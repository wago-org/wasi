//go:build darwin

package core

import (
	"os"
	"path"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func hostMountRoot(_ *os.File, host string) (string, error) { return host, nil }

const (
	hostOpenReadOnly           = unix.O_RDONLY
	hostOpenDirectory          = unix.O_DIRECTORY
	hostOpenNoFollow           = unix.O_NOFOLLOW
	hostOpenCanCreateDirectory = false
	hostOpenWriteAttributes    = 0
)

func openPreopen(path string, _ bool) (*os.File, error) { return os.Open(path) }

func openAt(d *fdEntry, name string, flags int, mode uint32) (*os.File, uint64) {
	return openAtDarwin(d, name, flags, mode, 0)
}

// openAtDarwin resolves every symlink from the preopen descriptor while
// opening every component with O_NOFOLLOW. O_RESOLVE_BENEATH is unavailable
// before macOS 15 and older kernels silently ignore its numeric flag, so a
// descriptor walk is required to preserve confinement on macOS 14.
func openAtDarwin(d *fdEntry, name string, flags int, mode uint32, symlinks int) (*os.File, uint64) {
	if symlinks > 40 {
		return nil, wasiELoop
	}
	if flags&unix.O_CREAT == 0 {
		mode = 0
	}
	rootFD, err := unix.Dup(int(d.file.Fd()))
	if err != nil {
		return nil, errno(err)
	}
	current := os.NewFile(uintptr(rootFD), d.file.Name())
	defer func() {
		if current != nil {
			_ = current.Close()
		}
	}()
	parts := strings.Split(name, "/")
	resolved := make([]string, 0, len(parts))
	for i, part := range parts {
		if part == "" || part == "." {
			continue
		}
		last := i == len(parts)-1
		openFlags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW
		openMode := uint32(0)
		if last {
			openFlags = flags | unix.O_CLOEXEC
			openMode = mode
			if flags&unix.O_SYMLINK == 0 {
				openFlags |= unix.O_NOFOLLOW
			}
		}
		fd, openErr := unix.Openat(int(current.Fd()), part, openFlags, openMode)
		if openErr == nil {
			next := os.NewFile(uintptr(fd), part)
			if last {
				_ = current.Close()
				current = nil
				return next, wasiOK
			}
			_ = current.Close()
			current = next
			resolved = append(resolved, part)
			continue
		}

		target, isSymlink, linkCode := darwinReadlink(current, part)
		if linkCode != wasiOK {
			return nil, linkCode
		}
		if !isSymlink {
			return nil, capabilityErr(openErr)
		}
		if last && flags&(unix.O_NOFOLLOW|unix.O_SYMLINK) != 0 {
			return nil, errno(openErr)
		}
		remaining := parts[i+1:]
		resolvedName, code := darwinResolveLink(resolved, target, remaining)
		if code != wasiOK {
			return nil, code
		}
		return openAtDarwin(d, resolvedName, flags, mode, symlinks+1)
	}
	fd, err := unix.Openat(int(current.Fd()), ".", flags|unix.O_CLOEXEC|unix.O_NOFOLLOW, mode)
	if err != nil {
		return nil, capabilityErr(err)
	}
	return os.NewFile(uintptr(fd), name), wasiOK
}

func darwinReadlink(parent *os.File, name string) (string, bool, uint64) {
	buf := make([]byte, 4096)
	n, err := unix.Readlinkat(int(parent.Fd()), name, buf)
	if err != nil {
		if err == syscall.EINVAL {
			return "", false, wasiOK
		}
		return "", false, errno(err)
	}
	if n == len(buf) {
		return "", false, wasiENametoolong
	}
	return string(buf[:n]), true, wasiOK
}

func darwinResolveLink(prefix []string, target string, remaining []string) (string, uint64) {
	if path.IsAbs(target) {
		return "", wasiENotcapable
	}
	parts := append([]string(nil), prefix...)
	for _, part := range strings.Split(target, "/") {
		switch part {
		case "", ".":
		case "..":
			if len(parts) == 0 {
				return "", wasiENotcapable
			}
			parts = parts[:len(parts)-1]
		default:
			parts = append(parts, part)
		}
	}
	parts = append(parts, remaining...)
	if len(parts) == 0 {
		return ".", wasiOK
	}
	return strings.Join(parts, "/"), wasiOK
}

func openMetadataAt(d *fdEntry, name string, follow bool) (*os.File, uint64) {
	flags := unix.O_EVTONLY
	if !follow {
		flags |= unix.O_SYMLINK
	}
	return openAt(d, name, flags, 0)
}

func directoryEntryInfo(parent *fdEntry, entry os.DirEntry) (os.FileInfo, uint64) {
	f, code := openMetadataAt(parent, entry.Name(), false)
	if code != wasiOK {
		return nil, code
	}
	defer f.Close()
	info, err := f.Stat()
	return info, errno(err)
}

func hostFileStat(info os.FileInfo) (dev, ino, nlink uint64, atim, ctim int64) {
	dev, ino, nlink = 1, 1, 1
	atim, ctim = info.ModTime().UnixNano(), info.ModTime().UnixNano()
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		dev, ino, nlink = uint64(st.Dev), st.Ino, uint64(st.Nlink)
		atim = st.Atimespec.Sec*1e9 + st.Atimespec.Nsec
		ctim = st.Ctimespec.Sec*1e9 + st.Ctimespec.Nsec
	}
	return
}

func hostAccessTime(info os.FileInfo) time.Time {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Atimespec.Sec, st.Atimespec.Nsec)
	}
	return info.ModTime()
}

func setFileTimes(file *os.File, times []time.Time) error {
	timevals := []unix.Timeval{
		unix.NsecToTimeval(times[0].UnixNano()),
		unix.NsecToTimeval(times[1].UnixNano()),
	}
	return unix.Futimes(int(file.Fd()), timevals)
}

func setPathTimes(parent *os.File, leaf string, times []time.Time, noFollow bool) error {
	flags := 0
	if noFollow {
		flags = unix.AT_SYMLINK_NOFOLLOW
	}
	values := []unix.Timespec{unix.NsecToTimespec(times[0].UnixNano()), unix.NsecToTimespec(times[1].UnixNano())}
	return unix.UtimesNanoAt(int(parent.Fd()), leaf, values, flags)
}

func hostInode(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}

func setHostFileFlags(_ *fdEntry, file *os.File, flags uint16) error {
	current, err := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	if err != nil {
		return err
	}
	current &^= unix.O_APPEND | unix.O_NONBLOCK
	if flags&1 != 0 {
		current |= unix.O_APPEND
	}
	if flags&4 != 0 {
		current |= unix.O_NONBLOCK
	}
	_, err = unix.FcntlInt(file.Fd(), unix.F_SETFL, current)
	return err
}

func appendWriteAt(file *os.File, b []byte, offset int64) (int, error) {
	return unix.Pwrite(int(file.Fd()), b, offset)
}

func makeDirectoryAt(parent *os.File, name string, mode uint32) error {
	return unix.Mkdirat(int(parent.Fd()), name, mode)
}

func linkAt(oldParent *os.File, oldName string, newParent *os.File, newName string) error {
	return unix.Linkat(int(oldParent.Fd()), oldName, int(newParent.Fd()), newName, 0)
}

func readlinkAt(parent *os.File, name string, buf []byte) (int, error) {
	return unix.Readlinkat(int(parent.Fd()), name, buf)
}

func removeAt(parent *os.File, name string, directory bool) error {
	flags := 0
	if directory {
		flags = unix.AT_REMOVEDIR
	}
	return unix.Unlinkat(int(parent.Fd()), name, flags)
}

func renameAt(oldParent *os.File, oldName string, newParent *os.File, newName string) error {
	err := unix.Renameat(int(oldParent.Fd()), oldName, int(newParent.Fd()), newName)
	// renameat(2) permits EEXIST for a non-empty target directory, but WASI expects ENOTEMPTY.
	if err == unix.EEXIST {
		return unix.ENOTEMPTY
	}
	return err
}

func symlinkAt(target string, parent *os.File, name string) error {
	return unix.Symlinkat(target, int(parent.Fd()), name)
}

func linkAtFollow(_ *fdEntry, _ string, _ *os.File, _ string) uint64 {
	// Darwin has no AT_EMPTY_PATH equivalent for linking an already resolved
	// descriptor. Reject rather than reintroduce a path race after validation.
	return wasiENotsup
}

func allocateFile(file *os.File, offset, length int64) error {
	if offset < 0 || length < 0 || offset > int64(maxInt64Value)-length {
		return unix.EINVAL
	}
	end := offset + length
	var info unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &info); err != nil {
		return err
	}
	if info.Mode&unix.S_IFMT != unix.S_IFREG {
		store := unix.Fstore_t{Flags: unix.F_ALLOCATEALL, Posmode: unix.F_PEOFPOSMODE, Length: end}
		return unix.FcntlFstore(file.Fd(), unix.F_PREALLOCATE, &store)
	}
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	if err != nil {
		return err
	}
	if flags&unix.O_ACCMODE == unix.O_RDONLY {
		return unix.EBADF
	}
	if info.Blocks < 0 || info.Blocks > int64(maxInt64Value)/512 {
		return unix.EOVERFLOW
	}
	// st_blocks counts allocated 512-byte blocks, including preallocation.
	// Logical size cannot measure physical EOF: a sparse file may be much larger.
	allocated := info.Blocks * 512
	// Aggregate block counts can include reservations past logical EOF, so they
	// cannot prove that the existing portion of this range has no holes.
	if err := requireDarwinBacking(file, offset, end, info.Size); err != nil {
		return err
	}
	if end > allocated {
		// F_PEOFPOSMODE requires offset zero and adds Length to physical EOF.
		// Request only the missing capacity, so repeated calls do not grow it.
		store := unix.Fstore_t{Flags: unix.F_ALLOCATEALL, Posmode: unix.F_PEOFPOSMODE, Length: end - allocated}
		if err := unix.FcntlFstore(file.Fd(), unix.F_PREALLOCATE, &store); err != nil {
			return err
		}
	}
	if info.Size < end {
		return file.Truncate(end)
	}
	return nil
}

// requireDarwinBacking conservatively accepts only existing ranges for which
// the filesystem reports no holes. SEEK_HOLE may report unwritten allocated
// extents as holes; reject these too rather than infer backing from st_blocks.
// F_PREALLOCATE cannot select an arbitrary existing hole without changing data.
func requireDarwinBacking(file *os.File, offset, end, size int64) error {
	if offset >= size || offset == end {
		return nil
	}
	existingEnd := end
	if existingEnd > size {
		existingEnd = size
	}
	fd := int(file.Fd())
	original, err := unix.Seek(fd, 0, unix.SEEK_CUR)
	if err != nil {
		return err
	}
	hole, queryErr := unix.Seek(fd, offset, unix.SEEK_HOLE)
	// Both successful and failed queries must leave the caller's offset intact.
	if _, err := unix.Seek(fd, original, unix.SEEK_SET); err != nil {
		return err
	}
	if queryErr != nil {
		switch queryErr {
		case unix.EINVAL, unix.EOPNOTSUPP, unix.ENOTTY, unix.ENOSYS:
			return unix.EOPNOTSUPP
		default:
			return queryErr
		}
	}
	if hole < existingEnd {
		return unix.EOPNOTSUPP
	}
	return nil
}

func setFileSize(file *os.File, size int64) error { return file.Truncate(size) }
