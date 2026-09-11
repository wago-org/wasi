package p2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	component "github.com/wago-org/component-model"
)

const (
	directoryStreamResource uint32 = 8
	fileStreamRepMin        uint32 = 0x10000
)

const (
	fsErrAccess uint32 = iota
	fsErrWouldBlock
	fsErrAlready
	fsErrBadDescriptor
	fsErrBusy
	fsErrDeadlock
	fsErrQuota
	fsErrExist
	fsErrFileTooLarge
	fsErrIllegalByteSequence
	fsErrInProgress
	fsErrInterrupted
	fsErrInvalid
	fsErrIO
	fsErrIsDirectory
	fsErrLoop
	fsErrTooManyLinks
	fsErrMessageSize
	fsErrNameTooLong
	fsErrNoDevice
	fsErrNoEntry
	fsErrNoLock
	fsErrInsufficientMemory
	fsErrInsufficientSpace
	fsErrNotDirectory
	fsErrNotEmpty
	fsErrNotRecoverable
	fsErrUnsupported
	fsErrNoTTY
	fsErrNoSuchDevice
	fsErrOverflow
	fsErrNotPermitted
	fsErrPipe
	fsErrReadOnly
	fsErrInvalidSeek
	fsErrTextFileBusy
	fsErrCrossDevice
)

const (
	descriptorUnknown uint32 = iota
	descriptorBlockDevice
	descriptorCharacterDevice
	descriptorDirectory
	descriptorFIFO
	descriptorSymbolicLink
	descriptorRegularFile
	descriptorSocket
)

type filesystemMount struct {
	guest string
	host  string
	flags uint32
	base  *os.File
}

type descriptorNode struct {
	file   *os.File
	mount  int
	flags  uint32
	isDir  bool
	append *appendTarget
}

type appendTarget struct {
	mu sync.Mutex
}

type fileStream struct {
	mu     sync.Mutex
	file   *os.File
	pos    int64
	read   bool
	append *appendTarget
}

type directoryStream struct {
	mu   sync.Mutex
	file *os.File
}

type filesystemState struct {
	mu         sync.Mutex
	resources  *component.HandleTable
	mounts     []filesystemMount
	descs      map[uint32]*descriptorNode
	streams    map[uint32]*fileStream
	dirs       map[uint32]*directoryStream
	nextDesc   uint32
	nextStream uint32
	nextDir    uint32
	limits     Limits
	wall       func() time.Time
	ioState    *hostState
}

func requireDirectoryMutation(n *descriptorNode) error {
	if n == nil || n.flags&(1<<5) == 0 {
		return hostFS.EROFS
	}
	return nil
}

func validateChildFlags(base *descriptorNode, requested, openFlags uint32) error {
	if requested&^uint32(0x3f) != 0 {
		return hostFS.EINVAL
	}
	if requested&1 != 0 && base.flags&1 == 0 || requested&2 != 0 && base.flags&2 == 0 || requested&(1<<5) != 0 && base.flags&(1<<5) == 0 {
		return hostFS.EROFS
	}
	if requested&0x1c != 0 && base.flags&2 == 0 {
		return hostFS.EROFS
	}
	if openFlags&(1<<3) != 0 && requested&2 == 0 {
		return hostFS.EINVAL
	}
	if openFlags&(1|1<<3) != 0 || requested&2 != 0 {
		return requireDirectoryMutation(base)
	}
	return nil
}

func checkedOffset(offset uint64) (int64, error) {
	if offset > math.MaxInt64 {
		return 0, hostFS.EOVERFLOW
	}
	return int64(offset), nil
}

func newFilesystem(configured []Preopen, limits Limits) *filesystemState {
	mounts := make([]filesystemMount, 0, len(configured))
	for _, mount := range configured {
		var flags uint32
		if mount.Read {
			flags |= 1
		}
		if mount.Write {
			flags |= 2
		}
		if mount.MutateDirectory {
			flags |= 1 << 5
		}
		mounts = append(mounts, filesystemMount{guest: mount.GuestPath, host: mount.HostPath, flags: flags})
	}
	sort.Slice(mounts, func(i, j int) bool { return mounts[i].guest < mounts[j].guest })
	return &filesystemState{mounts: mounts, descs: map[uint32]*descriptorNode{}, streams: map[uint32]*fileStream{}, dirs: map[uint32]*directoryStream{}, nextDesc: 1, nextStream: fileStreamRepMin, nextDir: 1, limits: limits.normalized(), wall: time.Now}
}

func prepareFilesystem(configured []Preopen, limits Limits) (*filesystemState, error) {
	s := newFilesystem(configured, limits)
	for i := range s.mounts {
		mount := &s.mounts[i]
		f, err := openPreopenDirectory(mount.host)
		if err != nil {
			s.closeMounts()
			return nil, fmt.Errorf("wasi p2: preopen %q: %w", mount.guest, err)
		}
		info, err := f.Stat()
		if err != nil || !info.IsDir() {
			f.Close()
			s.closeMounts()
			if err != nil {
				return nil, fmt.Errorf("wasi p2: preopen %q: %w", mount.guest, err)
			}
			return nil, fmt.Errorf("wasi p2: preopen %q host path is not a directory", mount.guest)
		}
		mount.base = f
	}
	return s, nil
}

func (s *filesystemState) closeMounts() {
	for i := range s.mounts {
		if s.mounts[i].base != nil {
			_ = s.mounts[i].base.Close()
			s.mounts[i].base = nil
		}
	}
}

func (s *filesystemState) addDesc(n *descriptorNode) (uint32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if uint32(len(s.descs)) >= s.limits.MaxDescriptors {
		return 0, hostFS.EMFILE
	}
	if !n.isDir && n.append == nil {
		n.append = &appendTarget{}
	}
	rep := s.nextDesc
	s.nextDesc++
	s.descs[rep] = n
	return rep, nil
}

func (s *filesystemState) desc(rep uint32) (*descriptorNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.descs[rep]
	if n == nil {
		return nil, fmt.Errorf("unknown descriptor rep %d", rep)
	}
	return n, nil
}

func (s *filesystemState) addStream(n *fileStream) (uint32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if uint32(len(s.streams)) >= s.limits.MaxStreams {
		return 0, hostFS.EMFILE
	}
	rep := s.nextStream
	s.nextStream++
	s.streams[rep] = n
	return rep, nil
}

func (s *filesystemState) output(rep uint32) io.Writer {
	s.mu.Lock()
	defer s.mu.Unlock()
	stream := s.streams[rep]
	if stream == nil || stream.read {
		return nil
	}
	return stream
}

func (s *filesystemState) input(rep uint32) io.Reader {
	s.mu.Lock()
	defer s.mu.Unlock()
	stream := s.streams[rep]
	if stream == nil || !stream.read {
		return nil
	}
	return stream.file
}

func (s *fileStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.append != nil {
		s.append.mu.Lock()
		defer s.append.mu.Unlock()
		info, err := s.file.Stat()
		if err != nil {
			return 0, err
		}
		return s.file.WriteAt(p, info.Size())
	}
	n, err := s.file.WriteAt(p, s.pos)
	s.pos += int64(n)
	return n, err
}

func (s *filesystemState) readStream(rep uint32, length uint64) ([]component.Value, error) {
	s.mu.Lock()
	stream := s.streams[rep]
	s.mu.Unlock()
	if stream == nil || !stream.read {
		return nil, fmt.Errorf("input-stream.read: unknown self %d", rep)
	}
	if length > s.limits.ioLimit() {
		length = s.limits.ioLimit()
	}
	if length == 0 {
		return []component.Value{component.ResultValue{Payload: []byte{}}}, nil
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	buf := make([]byte, int(length))
	n, err := stream.file.ReadAt(buf, stream.pos)
	stream.pos += int64(n)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("input-stream.read: %w", err)
	}
	if n == 0 {
		return []component.Value{component.ResultValue{IsErr: true, Payload: component.VariantValue{Disc: 1}}}, nil
	}
	return []component.Value{component.ResultValue{Payload: buf[:n]}}, nil
}

func dupFile(f *os.File) (*os.File, error) {
	fd, err := hostFS.Dup(int(f.Fd()))
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), f.Name()), nil
}

func splitRelative(name string) ([]string, error) {
	if strings.IndexByte(name, 0) >= 0 || path.IsAbs(name) {
		return nil, hostFS.EPERM
	}
	clean := path.Clean(name)
	if clean == "." {
		return nil, nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return nil, hostFS.EPERM
	}
	return strings.Split(clean, "/"), nil
}

// openUnder resolves every intermediate component with O_NOFOLLOW. This is a
// deliberately conservative capability boundary: symlinks are rejected
// instead of risking resolution outside the mounted directory.
func openUnder(dir *os.File, name string, flags int, mode uint32) (*os.File, error) {
	parts, err := splitRelative(name)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		fd, err := hostFS.Openat(int(dir.Fd()), ".", flags|hostFS.O_NOFOLLOW|hostFS.O_CLOEXEC, mode)
		if err != nil {
			return nil, err
		}
		return os.NewFile(uintptr(fd), dir.Name()), nil
	}
	cur, err := dupFile(dir)
	if err != nil {
		return nil, err
	}
	for _, part := range parts[:len(parts)-1] {
		fd, err := hostFS.Openat(int(cur.Fd()), part, hostFS.O_RDONLY|hostFS.O_DIRECTORY|hostFS.O_NOFOLLOW|hostFS.O_CLOEXEC, 0)
		cur.Close()
		if err != nil {
			return nil, err
		}
		cur = os.NewFile(uintptr(fd), part)
	}
	fd, err := hostFS.Openat(int(cur.Fd()), parts[len(parts)-1], flags|hostFS.O_NOFOLLOW|hostFS.O_CLOEXEC, mode)
	cur.Close()
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), parts[len(parts)-1]), nil
}

func parentUnder(dir *os.File, name string) (*os.File, string, error) {
	parts, err := splitRelative(name)
	if err != nil || len(parts) == 0 {
		if err == nil {
			err = hostFS.EPERM
		}
		return nil, "", err
	}
	parent := strings.Join(parts[:len(parts)-1], "/")
	f, err := openUnder(dir, parent, hostFS.O_RDONLY|hostFS.O_DIRECTORY, 0)
	return f, parts[len(parts)-1], err
}

func fsError(err error) uint32 {
	if code, ok := platformFilesystemError(err); ok {
		return code
	}
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, hostFS.ENOENT) {
		return fsErrNoEntry
	}
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, hostFS.EACCES) {
		return fsErrAccess
	}
	switch {
	case errors.Is(err, hostFS.EAGAIN):
		return fsErrWouldBlock
	case errors.Is(err, hostFS.EALREADY):
		return fsErrAlready
	case errors.Is(err, hostFS.EBADF):
		return fsErrBadDescriptor
	case errors.Is(err, hostFS.EBUSY):
		return fsErrBusy
	case errors.Is(err, hostFS.EDEADLK):
		return fsErrDeadlock
	case errors.Is(err, hostFS.EDQUOT):
		return fsErrQuota
	case errors.Is(err, hostFS.EEXIST):
		return fsErrExist
	case errors.Is(err, hostFS.EFBIG):
		return fsErrFileTooLarge
	case errors.Is(err, hostFS.EILSEQ):
		return fsErrIllegalByteSequence
	case errors.Is(err, hostFS.EINPROGRESS):
		return fsErrInProgress
	case errors.Is(err, hostFS.EINTR):
		return fsErrInterrupted
	case errors.Is(err, hostFS.EISDIR):
		return fsErrIsDirectory
	case errors.Is(err, hostFS.ENOTDIR):
		return fsErrNotDirectory
	case errors.Is(err, hostFS.ENOTEMPTY):
		return fsErrNotEmpty
	case errors.Is(err, hostFS.ELOOP):
		return fsErrLoop
	case errors.Is(err, hostFS.ENAMETOOLONG):
		return fsErrNameTooLong
	case errors.Is(err, hostFS.ENODEV):
		return fsErrNoDevice
	case errors.Is(err, hostFS.ENOLCK):
		return fsErrNoLock
	case errors.Is(err, hostFS.ENOMEM):
		return fsErrInsufficientMemory
	case errors.Is(err, hostFS.ENOSPC):
		return fsErrInsufficientSpace
	case errors.Is(err, hostFS.ENOTRECOVERABLE):
		return fsErrNotRecoverable
	case errors.Is(err, hostFS.ENOTSUP), errors.Is(err, hostFS.ENOSYS):
		return fsErrUnsupported
	case errors.Is(err, hostFS.ENOTTY):
		return fsErrNoTTY
	case errors.Is(err, hostFS.ENXIO):
		return fsErrNoSuchDevice
	case errors.Is(err, hostFS.EPERM):
		return fsErrNotPermitted
	case errors.Is(err, hostFS.EROFS):
		return fsErrReadOnly
	case errors.Is(err, hostFS.EXDEV):
		return fsErrCrossDevice
	case errors.Is(err, hostFS.EPIPE):
		return fsErrPipe
	case errors.Is(err, hostFS.ESPIPE):
		return fsErrInvalidSeek
	case errors.Is(err, hostFS.ETXTBSY):
		return fsErrTextFileBusy
	case errors.Is(err, hostFS.EINVAL):
		return fsErrInvalid
	case errors.Is(err, hostFS.EOVERFLOW):
		return fsErrOverflow
	case errors.Is(err, hostFS.EMFILE), errors.Is(err, hostFS.ENFILE):
		return fsErrQuota
	default:
		return fsErrIO
	}
}

func ok(v component.Value) []component.Value {
	return []component.Value{component.ResultValue{Payload: v}}
}
func fsFailure(err error) []component.Value {
	return []component.Value{component.ResultValue{IsErr: true, Payload: fsError(err)}}
}

func descriptorKind(info fs.FileInfo) uint32 {
	m := info.Mode()
	switch {
	case m.IsDir():
		return descriptorDirectory
	case m&fs.ModeSymlink != 0:
		return descriptorSymbolicLink
	case m&fs.ModeNamedPipe != 0:
		return descriptorFIFO
	case m&fs.ModeDevice != 0 && m&fs.ModeCharDevice != 0:
		return descriptorCharacterDevice
	case m&fs.ModeDevice != 0:
		return descriptorBlockDevice
	case m&fs.ModeSocket != 0:
		return descriptorSocket
	default:
		return descriptorRegularFile
	}
}

func statValue(info fs.FileInfo) component.Value {
	nlink, atime, mtime, ctime, _, _ := hostStat(info)
	return []component.Value{descriptorKind(info), nlink, uint64(info.Size()), datetime(atime), datetime(mtime), datetime(ctime)}
}

func metadataHash(info fs.FileInfo) component.Value {
	_, _, _, _, dev, ino := hostStat(info)
	if dev != 0 || ino != 0 {
		return []component.Value{ino, dev}
	}
	return []component.Value{uint64(info.ModTime().UnixNano()), uint64(info.Size())}
}

func datetime(t time.Time) component.Value {
	if t.Unix() < 0 {
		return nil
	}
	return []component.Value{uint64(t.Unix()), uint32(t.Nanosecond())}
}

func requestedTimes(access, modification component.Value, info fs.FileInfo, now func() time.Time) (time.Time, time.Time, bool, error) {
	_, currentAccess, currentModification, _, _, _ := hostStat(info)
	parse := func(v component.Value, current time.Time) (time.Time, bool, error) {
		x, ok := v.(component.VariantValue)
		if !ok {
			return time.Time{}, false, fmt.Errorf("new-timestamp is %T", v)
		}
		switch x.Disc {
		case 0:
			return current, false, nil
		case 1:
			return now(), true, nil
		case 2:
			r, ok := x.Payload.([]component.Value)
			if !ok || len(r) != 2 {
				return time.Time{}, false, hostFS.EINVAL
			}
			seconds, ok1 := r[0].(uint64)
			nanos, ok2 := r[1].(uint32)
			if !ok1 || !ok2 || seconds > math.MaxInt64 || nanos >= 1e9 {
				return time.Time{}, false, hostFS.EOVERFLOW
			}
			return time.Unix(int64(seconds), int64(nanos)), true, nil
		default:
			return time.Time{}, false, hostFS.EINVAL
		}
	}
	at, ac, e := parse(access, currentAccess)
	if e != nil {
		return time.Time{}, time.Time{}, false, e
	}
	mt, mc, e := parse(modification, currentModification)
	return at, mt, ac || mc, e
}

func filesystemOptions(s *filesystemState) []component.Option {
	getDirectories := func(context.Context, []component.Value) ([]component.Value, error) {
		out := make([]component.Value, 0, len(s.mounts))
		for i, mount := range s.mounts {
			var f *os.File
			var err error
			if mount.base != nil {
				f, err = dupFile(mount.base)
			} else {
				f, err = openPreopenDirectory(mount.host)
			}
			if err != nil {
				return nil, fmt.Errorf("preopen %q: %w", mount.guest, err)
			}
			info, err := f.Stat()
			if err != nil || !info.IsDir() {
				f.Close()
				return nil, fmt.Errorf("preopen %q is not a directory", mount.guest)
			}
			rep, addErr := s.addDesc(&descriptorNode{file: f, mount: i, flags: mount.flags, isDir: true})
			if addErr != nil {
				f.Close()
				return nil, addErr
			}
			h := s.resources.NewOwn(descriptorResource, rep)
			out = append(out, []component.Value{h, mount.guest})
		}
		return []component.Value{out}, nil
	}
	filesystemErrorCode := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 1 || s.ioState == nil {
			return []component.Value{nil}, nil
		}
		rep, ok := args[0].(uint32)
		if !ok {
			return nil, fmt.Errorf("filesystem-error-code: invalid error resource")
		}
		s.ioState.mu.Lock()
		value, exists := s.ioState.errors[rep]
		s.ioState.mu.Unlock()
		if !exists {
			return []component.Value{nil}, nil
		}
		return []component.Value{fsError(value.err)}, nil
	}
	openAt := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, err := s.desc(args[0].(uint32))
		if err != nil {
			return nil, err
		}
		if !n.isDir {
			return fsFailure(hostFS.ENOTDIR), nil
		}
		openFlags, descFlags := args[3].(uint32), args[4].(uint32)
		if e := validateChildFlags(n, descFlags, openFlags); e != nil {
			return fsFailure(e), nil
		}
		readable, writable := descFlags&1 != 0, descFlags&2 != 0
		flags := hostFS.O_RDONLY
		if readable && writable {
			flags = hostFS.O_RDWR
		} else if writable {
			flags = hostFS.O_WRONLY
		}
		if openFlags&(1<<1) != 0 {
			flags = hostFS.O_RDONLY | hostFS.O_DIRECTORY
			writable = false
		}
		if openFlags&1 != 0 {
			flags |= hostFS.O_CREAT
		}
		if openFlags&(1<<2) != 0 {
			flags |= hostFS.O_EXCL
		}
		if openFlags&(1<<3) != 0 && writable {
			flags |= hostFS.O_TRUNC
		}
		f, err := openUnder(n.file, args[2].(string), flags, 0o644)
		if err != nil {
			return fsFailure(err), nil
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return fsFailure(err), nil
		}
		rep, addErr := s.addDesc(&descriptorNode{file: f, mount: n.mount, flags: descFlags, isDir: info.IsDir()})
		if addErr != nil {
			f.Close()
			return fsFailure(addErr), nil
		}
		return ok(s.resources.NewOwn(descriptorResource, rep)), nil
	}
	getType := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		i, e := n.file.Stat()
		if e != nil {
			return fsFailure(e), nil
		}
		return ok(descriptorKind(i)), nil
	}
	getFlags := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		return ok(n.flags), nil
	}
	stat := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		i, e := n.file.Stat()
		if e != nil {
			return fsFailure(e), nil
		}
		return ok(statValue(i)), nil
	}
	statAt := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		f, e := openUnder(n.file, args[2].(string), hostFS.O_RDONLY, 0)
		if e != nil {
			return fsFailure(e), nil
		}
		defer f.Close()
		i, e := f.Stat()
		if e != nil {
			return fsFailure(e), nil
		}
		return ok(statValue(i)), nil
	}
	hash := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		i, e := n.file.Stat()
		if e != nil {
			return fsFailure(e), nil
		}
		return ok(metadataHash(i)), nil
	}
	hashAt := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		f, e := openUnder(n.file, args[2].(string), hostFS.O_RDONLY, 0)
		if e != nil {
			return fsFailure(e), nil
		}
		defer f.Close()
		i, e := f.Stat()
		if e != nil {
			return fsFailure(e), nil
		}
		return ok(metadataHash(i)), nil
	}
	readViaStream := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		if n.flags&1 == 0 {
			return fsFailure(hostFS.EBADF), nil
		}
		f, e := dupFile(n.file)
		if e != nil {
			return fsFailure(e), nil
		}
		offset, e := checkedOffset(args[1].(uint64))
		if e != nil {
			f.Close()
			return fsFailure(e), nil
		}
		rep, addErr := s.addStream(&fileStream{file: f, pos: offset, read: true})
		if addErr != nil {
			f.Close()
			return fsFailure(addErr), nil
		}
		return ok(s.resources.NewOwn(inputStreamResource, rep)), nil
	}
	writeViaStream := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		if n.flags&2 == 0 {
			return fsFailure(hostFS.EBADF), nil
		}
		f, e := dupFile(n.file)
		if e != nil {
			return fsFailure(e), nil
		}
		offset, e := checkedOffset(args[1].(uint64))
		if e != nil {
			f.Close()
			return fsFailure(e), nil
		}
		rep, addErr := s.addStream(&fileStream{file: f, pos: offset})
		if addErr != nil {
			f.Close()
			return fsFailure(addErr), nil
		}
		return ok(s.resources.NewOwn(outputStreamResource, rep)), nil
	}
	appendViaStream := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		if n.flags&2 == 0 {
			return fsFailure(hostFS.EBADF), nil
		}
		f, e := dupFile(n.file)
		if e != nil {
			return fsFailure(e), nil
		}
		rep, addErr := s.addStream(&fileStream{file: f, append: n.append})
		if addErr != nil {
			f.Close()
			return fsFailure(addErr), nil
		}
		return ok(s.resources.NewOwn(outputStreamResource, rep)), nil
	}
	readDirectory := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		if !n.isDir {
			return fsFailure(hostFS.ENOTDIR), nil
		}
		f, e := dupFile(n.file)
		if e != nil {
			return fsFailure(e), nil
		}
		s.mu.Lock()
		if uint32(len(s.dirs)) >= s.limits.MaxDirectoryStreams {
			s.mu.Unlock()
			f.Close()
			return fsFailure(hostFS.EMFILE), nil
		}
		rep := s.nextDir
		s.nextDir++
		s.dirs[rep] = &directoryStream{file: f}
		s.mu.Unlock()
		return ok(s.resources.NewOwn(directoryStreamResource, rep)), nil
	}
	readDirectoryEntry := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		s.mu.Lock()
		d := s.dirs[args[0].(uint32)]
		s.mu.Unlock()
		if d == nil {
			return nil, fmt.Errorf("unknown directory stream")
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		entries, readErr := d.file.ReadDir(1)
		if errors.Is(readErr, io.EOF) || len(entries) == 0 {
			return ok(nil), nil
		}
		if readErr != nil {
			return fsFailure(readErr), nil
		}
		entry := entries[0]
		if uint64(len(entry.Name())) > s.limits.MaxDirectoryEntryBytes {
			return fsFailure(hostFS.ENAMETOOLONG), nil
		}
		i, e := entry.Info()
		if e != nil {
			return fsFailure(e), nil
		}
		return ok([]component.Value{descriptorKind(i), entry.Name()}), nil
	}
	createDirectoryAt := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		if n.flags&(1<<5) == 0 {
			return fsFailure(hostFS.EROFS), nil
		}
		p, name, e := parentUnder(n.file, args[1].(string))
		if e != nil {
			return fsFailure(e), nil
		}
		defer p.Close()
		e = hostFS.Mkdirat(int(p.Fd()), name, 0o755)
		if e != nil {
			return fsFailure(e), nil
		}
		return ok(nil), nil
	}
	removeAt := func(dir bool) component.HostFunc {
		return func(_ context.Context, args []component.Value) ([]component.Value, error) {
			n, e := s.desc(args[0].(uint32))
			if e != nil {
				return nil, e
			}
			if n.flags&(1<<5) == 0 {
				return fsFailure(hostFS.EROFS), nil
			}
			p, name, e := parentUnder(n.file, args[1].(string))
			if e != nil {
				return fsFailure(e), nil
			}
			defer p.Close()
			flags := 0
			if dir {
				flags = hostFS.AT_REMOVEDIR
			}
			e = hostFS.Unlinkat(int(p.Fd()), name, flags)
			if e != nil {
				return fsFailure(e), nil
			}
			return ok(nil), nil
		}
	}
	renameAt := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		a, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		b, e := s.desc(args[2].(uint32))
		if e != nil {
			return nil, e
		}
		if a.flags&(1<<5) == 0 || b.flags&(1<<5) == 0 {
			return fsFailure(hostFS.EROFS), nil
		}
		if a.mount != b.mount {
			return fsFailure(hostFS.EXDEV), nil
		}
		ap, an, e := parentUnder(a.file, args[1].(string))
		if e != nil {
			return fsFailure(e), nil
		}
		defer ap.Close()
		bp, bn, e := parentUnder(b.file, args[3].(string))
		if e != nil {
			return fsFailure(e), nil
		}
		defer bp.Close()
		e = hostFS.Renameat(int(ap.Fd()), an, int(bp.Fd()), bn)
		if e != nil {
			return fsFailure(e), nil
		}
		return ok(nil), nil
	}
	syncFile := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		if e = n.file.Sync(); e != nil {
			return fsFailure(e), nil
		}
		return ok(nil), nil
	}
	syncData := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		if e = syncFileData(n.file); e != nil {
			return fsFailure(e), nil
		}
		return ok(nil), nil
	}
	advise := func(context.Context, []component.Value) ([]component.Value, error) {
		return fsFailure(hostFS.ENOTSUP), nil
	}
	setSize := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		if n.flags&2 == 0 {
			return fsFailure(hostFS.EROFS), nil
		}
		size, e := checkedOffset(args[1].(uint64))
		if e == nil {
			e = n.file.Truncate(size)
		}
		if e != nil {
			return fsFailure(e), nil
		}
		return ok(nil), nil
	}
	directRead := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		if n.flags&1 == 0 {
			return fsFailure(hostFS.EBADF), nil
		}
		length := args[1].(uint64)
		if length > s.limits.ioLimit() {
			length = s.limits.ioLimit()
		}
		offset, e := checkedOffset(args[2].(uint64))
		if e != nil {
			return fsFailure(e), nil
		}
		buf := make([]byte, int(length))
		got, readErr := n.file.ReadAt(buf, offset)
		eof := errors.Is(readErr, io.EOF)
		if readErr != nil && !eof {
			return fsFailure(readErr), nil
		}
		return ok([]component.Value{buf[:got], eof}), nil
	}
	directWrite := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		if n.flags&2 == 0 {
			return fsFailure(hostFS.EROFS), nil
		}
		buf, e := bytesValue(args[1])
		if e != nil {
			return nil, e
		}
		offset, e := checkedOffset(args[2].(uint64))
		if e != nil {
			return fsFailure(e), nil
		}
		got, e := n.file.WriteAt(buf, offset)
		if e == nil && got != len(buf) {
			e = io.ErrShortWrite
		}
		if e != nil {
			return fsFailure(e), nil
		}
		return ok(uint64(got)), nil
	}
	setTimes := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		if n.flags&2 == 0 {
			return fsFailure(hostFS.EROFS), nil
		}
		info, e := n.file.Stat()
		if e != nil {
			return fsFailure(e), nil
		}
		at, mt, _, e := requestedTimes(args[1], args[2], info, s.wall)
		if e == nil {
			e = setFileTimes(n.file, at, mt)
		}
		if e != nil {
			return fsFailure(e), nil
		}
		return ok(nil), nil
	}
	setTimesAt := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		if e = requireDirectoryMutation(n); e != nil {
			return fsFailure(e), nil
		}
		f, e := openUnder(n.file, args[2].(string), hostFS.O_RDONLY, 0)
		if e != nil {
			return fsFailure(e), nil
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil {
			return fsFailure(e), nil
		}
		at, mt, _, e := requestedTimes(args[3], args[4], info, s.wall)
		if e == nil {
			e = setFileTimes(f, at, mt)
		}
		if e != nil {
			return fsFailure(e), nil
		}
		return ok(nil), nil
	}
	linkAt := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		a, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		b, e := s.desc(args[3].(uint32))
		if e != nil {
			return nil, e
		}
		if e = requireDirectoryMutation(b); e != nil {
			return fsFailure(e), nil
		}
		if a.mount != b.mount {
			return fsFailure(hostFS.EXDEV), nil
		}
		if args[1].(uint32)&1 != 0 {
			return fsFailure(hostFS.ENOTSUP), nil
		}
		ap, an, e := parentUnder(a.file, args[2].(string))
		if e != nil {
			return fsFailure(e), nil
		}
		defer ap.Close()
		bp, bn, e := parentUnder(b.file, args[4].(string))
		if e != nil {
			return fsFailure(e), nil
		}
		defer bp.Close()
		e = hostFS.Linkat(int(ap.Fd()), an, int(bp.Fd()), bn, 0)
		if e != nil {
			return fsFailure(e), nil
		}
		return ok(nil), nil
	}
	readlinkAt := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		p, name, e := parentUnder(n.file, args[1].(string))
		if e != nil {
			return fsFailure(e), nil
		}
		defer p.Close()
		buf := make([]byte, 4096)
		for {
			got, e := hostFS.Readlinkat(int(p.Fd()), name, buf)
			if e != nil {
				return fsFailure(e), nil
			}
			if got < len(buf) {
				return ok(string(buf[:got])), nil
			}
			if len(buf) >= maxIOSize {
				return fsFailure(hostFS.ENAMETOOLONG), nil
			}
			buf = make([]byte, len(buf)*2)
		}
	}
	symlinkAt := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		if e = requireDirectoryMutation(n); e != nil {
			return fsFailure(e), nil
		}
		p, name, e := parentUnder(n.file, args[2].(string))
		if e != nil {
			return fsFailure(e), nil
		}
		defer p.Close()
		e = hostFS.Symlinkat(args[1].(string), int(p.Fd()), name)
		if e != nil {
			return fsFailure(e), nil
		}
		return ok(nil), nil
	}
	isSame := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		a, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		b, e := s.desc(args[1].(uint32))
		if e != nil {
			return nil, e
		}
		ai, e := a.file.Stat()
		if e != nil {
			return nil, e
		}
		bi, e := b.file.Stat()
		if e != nil {
			return nil, e
		}
		return []component.Value{os.SameFile(ai, bi)}, nil
	}

	return []component.Option{
		component.WithResourceTag(ifaceFilesystem, "descriptor", descriptorResource),
		component.WithResourceTag(ifaceFilesystem, "directory-entry-stream", directoryStreamResource),
		component.WithHostResourceDtor(descriptorResource, func(_ context.Context, rep uint32) error {
			s.mu.Lock()
			n := s.descs[rep]
			delete(s.descs, rep)
			s.mu.Unlock()
			if n != nil {
				return n.file.Close()
			}
			return nil
		}),
		component.WithHostResourceDtor(inputStreamResource, func(_ context.Context, rep uint32) error { return s.dropStream(rep) }),
		component.WithHostResourceDtor(outputStreamResource, func(_ context.Context, rep uint32) error {
			if rep == stdoutRep || rep == stderrRep {
				return nil
			}
			return s.dropStream(rep)
		}),
		component.WithHostResourceDtor(directoryStreamResource, func(_ context.Context, rep uint32) error {
			s.mu.Lock()
			d := s.dirs[rep]
			delete(s.dirs, rep)
			s.mu.Unlock()
			if d != nil {
				return d.file.Close()
			}
			return nil
		}),
		custom(ifacePreopens, "get-directories", getDirectories, func(t *component.TypeTable) component.FuncDesc {
			return t.Func(nil, t.List(t.Tuple(t.Own(descriptorResource), component.Prim("string"))))
		}),
		custom(ifaceFilesystem, "filesystem-error-code", filesystemErrorCode, filesystemErrorCodeDesc),
		custom(ifaceFilesystem, "[method]descriptor.open-at", openAt, openAtDesc),
		custom(ifaceFilesystem, "[method]descriptor.get-type", getType, getTypeDesc),
		custom(ifaceFilesystem, "[method]descriptor.get-flags", getFlags, getFlagsDesc),
		custom(ifaceFilesystem, "[method]descriptor.stat", stat, statDesc),
		custom(ifaceFilesystem, "[method]descriptor.stat-at", statAt, statAtDesc),
		custom(ifaceFilesystem, "[method]descriptor.metadata-hash", hash, metadataHashDesc),
		custom(ifaceFilesystem, "[method]descriptor.metadata-hash-at", hashAt, metadataHashAtDesc),
		custom(ifaceFilesystem, "[method]descriptor.read-via-stream", readViaStream, readViaStreamDesc),
		custom(ifaceFilesystem, "[method]descriptor.write-via-stream", writeViaStream, writeViaStreamDesc),
		custom(ifaceFilesystem, "[method]descriptor.append-via-stream", appendViaStream, appendViaStreamDesc),
		custom(ifaceFilesystem, "[method]descriptor.read-directory", readDirectory, readDirectoryDesc),
		custom(ifaceFilesystem, "[method]directory-entry-stream.read-directory-entry", readDirectoryEntry, readDirectoryEntryDesc),
		custom(ifaceFilesystem, "[method]descriptor.create-directory-at", createDirectoryAt, pathMutationDesc),
		custom(ifaceFilesystem, "[method]descriptor.unlink-file-at", removeAt(false), pathMutationDesc),
		custom(ifaceFilesystem, "[method]descriptor.remove-directory-at", removeAt(true), pathMutationDesc),
		custom(ifaceFilesystem, "[method]descriptor.rename-at", renameAt, renameAtDesc),
		custom(ifaceFilesystem, "[method]descriptor.sync", syncFile, syncDesc),
		custom(ifaceFilesystem, "[method]descriptor.sync-data", syncData, syncDesc),
		custom(ifaceFilesystem, "[method]descriptor.advise", advise, adviseDesc),
		custom(ifaceFilesystem, "[method]descriptor.set-size", setSize, setSizeDesc),
		custom(ifaceFilesystem, "[method]descriptor.set-times", setTimes, setTimesDesc),
		custom(ifaceFilesystem, "[method]descriptor.read", directRead, directReadDesc),
		custom(ifaceFilesystem, "[method]descriptor.write", directWrite, directWriteDesc),
		custom(ifaceFilesystem, "[method]descriptor.set-times-at", setTimesAt, setTimesAtDesc),
		custom(ifaceFilesystem, "[method]descriptor.link-at", linkAt, linkAtDesc),
		custom(ifaceFilesystem, "[method]descriptor.readlink-at", readlinkAt, readlinkAtDesc),
		custom(ifaceFilesystem, "[method]descriptor.symlink-at", symlinkAt, symlinkAtDesc),
		custom(ifaceFilesystem, "[method]descriptor.is-same-object", isSame, isSameDesc),
	}
}

func (s *filesystemState) dropStream(rep uint32) error {
	s.mu.Lock()
	n := s.streams[rep]
	delete(s.streams, rep)
	s.mu.Unlock()
	if s.ioState != nil {
		s.ioState.mu.Lock()
		delete(s.ioState.outputs, rep)
		delete(s.ioState.permits, rep)
		s.ioState.mu.Unlock()
	}
	if n != nil {
		return n.file.Close()
	}
	return nil
}

func errorCode(t *component.TypeTable) component.TypeRef {
	return t.Enum("access", "would-block", "already", "bad-descriptor", "busy", "deadlock", "quota", "exist", "file-too-large", "illegal-byte-sequence", "in-progress", "interrupted", "invalid", "io", "is-directory", "loop", "too-many-links", "message-size", "name-too-long", "no-device", "no-entry", "no-lock", "insufficient-memory", "insufficient-space", "not-directory", "not-empty", "not-recoverable", "unsupported", "no-tty", "no-such-device", "overflow", "not-permitted", "pipe", "read-only", "invalid-seek", "text-file-busy", "cross-device")
}
func descriptorType(t *component.TypeTable) component.TypeRef {
	return t.Enum("unknown", "block-device", "character-device", "directory", "fifo", "symbolic-link", "regular-file", "socket")
}
func descResult(t *component.TypeTable, ok component.TypeRef) component.TypeRef {
	return t.Result(ok, errorCode(t))
}
func filesystemErrorCodeDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(errorResource)}, t.Option(errorCode(t)))
}
func openAtDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource), t.Flags("symlink-follow"), component.Prim("string"), t.Flags("create", "directory", "exclusive", "truncate"), t.Flags("read", "write", "file-integrity-sync", "data-integrity-sync", "requested-write-sync", "mutate-directory")}, descResult(t, t.Own(descriptorResource)))
}
func getTypeDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource)}, descResult(t, descriptorType(t)))
}
func getFlagsDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource)}, descResult(t, t.Flags("read", "write", "file-integrity-sync", "data-integrity-sync", "requested-write-sync", "mutate-directory")))
}
func statRecord(t *component.TypeTable) component.TypeRef {
	dt := t.Record("seconds", component.Prim("u64"), "nanoseconds", component.Prim("u32"))
	return t.Record("type", descriptorType(t), "link-count", component.Prim("u64"), "size", component.Prim("u64"), "data-access-timestamp", t.Option(dt), "data-modification-timestamp", t.Option(dt), "status-change-timestamp", t.Option(dt))
}
func statDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource)}, descResult(t, statRecord(t)))
}
func statAtDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource), t.Flags("symlink-follow"), component.Prim("string")}, descResult(t, statRecord(t)))
}
func metadataHashType(t *component.TypeTable) component.TypeRef {
	return t.Record("lower", component.Prim("u64"), "upper", component.Prim("u64"))
}
func metadataHashDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource)}, descResult(t, metadataHashType(t)))
}
func metadataHashAtDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource), t.Flags("symlink-follow"), component.Prim("string")}, descResult(t, metadataHashType(t)))
}
func readViaStreamDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource), component.Prim("u64")}, descResult(t, t.Own(inputStreamResource)))
}
func writeViaStreamDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource), component.Prim("u64")}, descResult(t, t.Own(outputStreamResource)))
}
func appendViaStreamDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource)}, descResult(t, t.Own(outputStreamResource)))
}
func readDirectoryDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource)}, descResult(t, t.Own(directoryStreamResource)))
}
func readDirectoryEntryDesc(t *component.TypeTable) component.FuncDesc {
	entry := t.Record("type", descriptorType(t), "name", component.Prim("string"))
	return t.Func([]component.TypeRef{t.Borrow(directoryStreamResource)}, descResult(t, t.Option(entry)))
}
func pathMutationDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource), component.Prim("string")}, descResult(t, component.TypeRef{}))
}
func renameAtDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource), component.Prim("string"), t.Borrow(descriptorResource), component.Prim("string")}, descResult(t, component.TypeRef{}))
}
func syncDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource)}, descResult(t, component.TypeRef{}))
}
func adviceType(t *component.TypeTable) component.TypeRef {
	return t.Enum("normal", "sequential", "random", "will-need", "dont-need", "no-reuse")
}
func adviseDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource), component.Prim("u64"), component.Prim("u64"), adviceType(t)}, descResult(t, component.TypeRef{}))
}
func setSizeDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource), component.Prim("u64")}, descResult(t, component.TypeRef{}))
}
func newTimestampType(t *component.TypeTable) component.TypeRef {
	dt := t.Record("seconds", component.Prim("u64"), "nanoseconds", component.Prim("u32"))
	return t.Variant(component.VariantCaseSpec{Name: "no-change"}, component.VariantCaseSpec{Name: "now"}, component.VariantCaseSpec{Name: "timestamp", Type: dt})
}
func setTimesDesc(t *component.TypeTable) component.FuncDesc {
	nt := newTimestampType(t)
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource), nt, nt}, descResult(t, component.TypeRef{}))
}
func directReadDesc(t *component.TypeTable) component.FuncDesc {
	pair := t.Tuple(t.List(component.Prim("u8")), component.Prim("bool"))
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource), component.Prim("u64"), component.Prim("u64")}, descResult(t, pair))
}
func directWriteDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource), t.List(component.Prim("u8")), component.Prim("u64")}, descResult(t, component.Prim("u64")))
}
func setTimesAtDesc(t *component.TypeTable) component.FuncDesc {
	nt := newTimestampType(t)
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource), t.Flags("symlink-follow"), component.Prim("string"), nt, nt}, descResult(t, component.TypeRef{}))
}
func linkAtDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource), t.Flags("symlink-follow"), component.Prim("string"), t.Borrow(descriptorResource), component.Prim("string")}, descResult(t, component.TypeRef{}))
}
func readlinkAtDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource), component.Prim("string")}, descResult(t, component.Prim("string")))
}
func symlinkAtDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource), component.Prim("string"), component.Prim("string")}, descResult(t, component.TypeRef{}))
}
func isSameDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(descriptorResource), t.Borrow(descriptorResource)}, component.Prim("bool"))
}
