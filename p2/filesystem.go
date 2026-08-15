package p2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"syscall"

	component "github.com/wago-org/component-model"
	"golang.org/x/sys/unix"
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
}

type descriptorNode struct {
	file               *os.File
	mount              int
	readable, writable bool
	isDir              bool
}

type fileStream struct {
	mu   sync.Mutex
	file *os.File
	pos  int64
	read bool
}

type directoryStream struct {
	mu      sync.Mutex
	entries []fs.DirEntry
	pos     int
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
}

func newFilesystem(preopens map[string]string) *filesystemState {
	guestPaths := make([]string, 0, len(preopens))
	for guest := range preopens {
		guestPaths = append(guestPaths, guest)
	}
	sort.Strings(guestPaths)
	mounts := make([]filesystemMount, 0, len(guestPaths))
	for _, guest := range guestPaths {
		clean := path.Clean("/" + strings.TrimPrefix(guest, "/"))
		mounts = append(mounts, filesystemMount{guest: clean, host: preopens[guest]})
	}
	return &filesystemState{mounts: mounts, descs: map[uint32]*descriptorNode{}, streams: map[uint32]*fileStream{}, dirs: map[uint32]*directoryStream{}, nextDesc: 1, nextStream: fileStreamRepMin, nextDir: 1}
}

func (s *filesystemState) addDesc(n *descriptorNode) uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	rep := s.nextDesc
	s.nextDesc++
	s.descs[rep] = n
	return rep
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

func (s *filesystemState) addStream(n *fileStream) uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	rep := s.nextStream
	s.nextStream++
	s.streams[rep] = n
	return rep
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

func (s *fileStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	if length > maxIOSize {
		length = maxIOSize
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
	fd, err := unix.Dup(int(f.Fd()))
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), f.Name()), nil
}

func splitRelative(name string) ([]string, error) {
	if strings.IndexByte(name, 0) >= 0 || path.IsAbs(name) {
		return nil, unix.EPERM
	}
	clean := path.Clean(name)
	if clean == "." {
		return nil, nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return nil, unix.EPERM
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
		fd, err := unix.Openat(int(dir.Fd()), ".", flags|unix.O_NOFOLLOW|unix.O_CLOEXEC, mode)
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
		fd, err := unix.Openat(int(cur.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		cur.Close()
		if err != nil {
			return nil, err
		}
		cur = os.NewFile(uintptr(fd), part)
	}
	fd, err := unix.Openat(int(cur.Fd()), parts[len(parts)-1], flags|unix.O_NOFOLLOW|unix.O_CLOEXEC, mode)
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
			err = unix.EPERM
		}
		return nil, "", err
	}
	parent := strings.Join(parts[:len(parts)-1], "/")
	f, err := openUnder(dir, parent, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	return f, parts[len(parts)-1], err
}

func fsError(err error) uint32 {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOENT) {
		return fsErrNoEntry
	}
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, unix.EACCES) {
		return fsErrAccess
	}
	switch {
	case errors.Is(err, unix.EEXIST):
		return fsErrExist
	case errors.Is(err, unix.EISDIR):
		return fsErrIsDirectory
	case errors.Is(err, unix.ENOTDIR):
		return fsErrNotDirectory
	case errors.Is(err, unix.ENOTEMPTY):
		return fsErrNotEmpty
	case errors.Is(err, unix.ELOOP):
		return fsErrLoop
	case errors.Is(err, unix.ENAMETOOLONG):
		return fsErrNameTooLong
	case errors.Is(err, unix.EPERM):
		return fsErrNotPermitted
	case errors.Is(err, unix.EROFS):
		return fsErrReadOnly
	case errors.Is(err, unix.EXDEV):
		return fsErrCrossDevice
	case errors.Is(err, unix.EINVAL):
		return fsErrInvalid
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
	return []component.Value{descriptorKind(info), uint64(1), uint64(info.Size()), nil, nil, nil}
}

func metadataHash(info fs.FileInfo) component.Value {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return []component.Value{uint64(st.Ino), uint64(st.Dev)}
	}
	return []component.Value{uint64(info.ModTime().UnixNano()), uint64(info.Size())}
}

func filesystemOptions(s *filesystemState) []component.Option {
	getDirectories := func(context.Context, []component.Value) ([]component.Value, error) {
		out := make([]component.Value, 0, len(s.mounts))
		for i, mount := range s.mounts {
			f, err := os.Open(mount.host)
			if err != nil {
				return nil, fmt.Errorf("preopen %q: %w", mount.guest, err)
			}
			info, err := f.Stat()
			if err != nil || !info.IsDir() {
				f.Close()
				return nil, fmt.Errorf("preopen %q is not a directory", mount.guest)
			}
			rep := s.addDesc(&descriptorNode{file: f, mount: i, readable: true, isDir: true})
			h := s.resources.NewOwn(descriptorResource, rep)
			out = append(out, []component.Value{h, mount.guest})
		}
		return []component.Value{out}, nil
	}
	filesystemErrorCode := func(context.Context, []component.Value) ([]component.Value, error) {
		return []component.Value{nil}, nil
	}
	openAt := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, err := s.desc(args[0].(uint32))
		if err != nil {
			return nil, err
		}
		if !n.isDir {
			return fsFailure(unix.ENOTDIR), nil
		}
		openFlags, descFlags := args[3].(uint32), args[4].(uint32)
		readable, writable := descFlags&1 != 0, descFlags&2 != 0
		flags := unix.O_RDONLY
		if readable && writable {
			flags = unix.O_RDWR
		} else if writable {
			flags = unix.O_WRONLY
		} else {
			readable = true
		}
		if openFlags&(1<<1) != 0 {
			flags = unix.O_RDONLY | unix.O_DIRECTORY
			readable, writable = true, false
		}
		if openFlags&1 != 0 {
			flags |= unix.O_CREAT
		}
		if openFlags&(1<<2) != 0 {
			flags |= unix.O_EXCL
		}
		if openFlags&(1<<3) != 0 && writable {
			flags |= unix.O_TRUNC
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
		rep := s.addDesc(&descriptorNode{file: f, mount: n.mount, readable: readable, writable: writable, isDir: info.IsDir()})
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
		var f uint32
		if n.readable {
			f |= 1
		}
		if n.writable {
			f |= 2
		}
		if n.isDir {
			f |= 1 << 5
		}
		return ok(f), nil
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
		f, e := openUnder(n.file, args[2].(string), unix.O_RDONLY, 0)
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
		f, e := openUnder(n.file, args[2].(string), unix.O_RDONLY, 0)
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
		if !n.readable {
			return fsFailure(unix.EBADF), nil
		}
		f, e := dupFile(n.file)
		if e != nil {
			return fsFailure(e), nil
		}
		rep := s.addStream(&fileStream{file: f, pos: int64(args[1].(uint64)), read: true})
		return ok(s.resources.NewOwn(inputStreamResource, rep)), nil
	}
	writeViaStream := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		if !n.writable {
			return fsFailure(unix.EBADF), nil
		}
		f, e := dupFile(n.file)
		if e != nil {
			return fsFailure(e), nil
		}
		rep := s.addStream(&fileStream{file: f, pos: int64(args[1].(uint64))})
		return ok(s.resources.NewOwn(outputStreamResource, rep)), nil
	}
	appendViaStream := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		if !n.writable {
			return fsFailure(unix.EBADF), nil
		}
		i, e := n.file.Stat()
		if e != nil {
			return fsFailure(e), nil
		}
		f, e := dupFile(n.file)
		if e != nil {
			return fsFailure(e), nil
		}
		rep := s.addStream(&fileStream{file: f, pos: i.Size()})
		return ok(s.resources.NewOwn(outputStreamResource, rep)), nil
	}
	readDirectory := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		n, e := s.desc(args[0].(uint32))
		if e != nil {
			return nil, e
		}
		if !n.isDir {
			return fsFailure(unix.ENOTDIR), nil
		}
		f, e := dupFile(n.file)
		if e != nil {
			return fsFailure(e), nil
		}
		entries, e := f.ReadDir(-1)
		f.Close()
		if e != nil {
			return fsFailure(e), nil
		}
		s.mu.Lock()
		rep := s.nextDir
		s.nextDir++
		s.dirs[rep] = &directoryStream{entries: entries}
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
		if d.pos == len(d.entries) {
			return ok(nil), nil
		}
		entry := d.entries[d.pos]
		d.pos++
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
		p, name, e := parentUnder(n.file, args[1].(string))
		if e != nil {
			return fsFailure(e), nil
		}
		defer p.Close()
		e = unix.Mkdirat(int(p.Fd()), name, 0o755)
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
			p, name, e := parentUnder(n.file, args[1].(string))
			if e != nil {
				return fsFailure(e), nil
			}
			defer p.Close()
			flags := 0
			if dir {
				flags = unix.AT_REMOVEDIR
			}
			e = unix.Unlinkat(int(p.Fd()), name, flags)
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
		if a.mount != b.mount {
			return fsFailure(unix.EXDEV), nil
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
		e = unix.Renameat(int(ap.Fd()), an, int(bp.Fd()), bn)
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
			delete(s.dirs, rep)
			s.mu.Unlock()
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
		custom(ifaceFilesystem, "[method]descriptor.sync-data", syncFile, syncDesc),
	}
}

func (s *filesystemState) dropStream(rep uint32) error {
	s.mu.Lock()
	n := s.streams[rep]
	delete(s.streams, rep)
	s.mu.Unlock()
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
