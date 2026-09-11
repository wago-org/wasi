//go:build linux

package core

import (
	"errors"
	"os"
	"path"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func hostMountRoot(_ *os.File, host string) (string, error) { return host, nil }

const secureResolve = unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS

const (
	hostOpenReadOnly        = unix.O_RDONLY
	hostOpenDirectory       = unix.O_DIRECTORY
	hostOpenNoFollow        = unix.O_NOFOLLOW
	hostOpenWriteAttributes = 0
)

func openPreopen(path string, _ bool) (*os.File, error) { return os.Open(path) }

func openAt(d *fdEntry, name string, flags int, mode uint32) (*os.File, uint64) {
	if flags&unix.O_CREAT == 0 {
		mode = 0
	}
	fd, err := unix.Openat2(int(d.file.Fd()), name, &unix.OpenHow{
		Flags: uint64(flags | unix.O_CLOEXEC), Mode: uint64(mode), Resolve: secureResolve,
	})
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.E2BIG) || errors.Is(err, unix.EPERM) {
		return openAtWalk(d, name, flags, mode)
	}
	if err != nil {
		return nil, capabilityErr(err)
	}
	return os.NewFile(uintptr(fd), name), wasiOK
}

// openAtWalk is the secure fallback for kernels, seccomp profiles, and syscall
// emulators without openat2. It refuses symlinks rather than attempting a
// race-prone userspace emulation of RESOLVE_BENEATH.
func openAtWalk(d *fdEntry, name string, flags int, mode uint32) (*os.File, uint64) {
	clean := path.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return nil, wasiENotcapable
	}
	parts := strings.Split(clean, "/")
	curFD, err := unix.Dup(int(d.file.Fd()))
	if err != nil {
		return nil, errno(err)
	}
	cur := os.NewFile(uintptr(curFD), d.file.Name())
	defer func() { _ = cur.Close() }()
	for _, part := range parts[:len(parts)-1] {
		if part == "." || part == "" {
			continue
		}
		next, err := unix.Openat(int(cur.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, capabilityErr(err)
		}
		cur.Close()
		cur = os.NewFile(uintptr(next), part)
	}
	leaf := parts[len(parts)-1]
	fd, err := unix.Openat(int(cur.Fd()), leaf, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC, mode)
	if err != nil {
		return nil, capabilityErr(err)
	}
	return os.NewFile(uintptr(fd), name), wasiOK
}

func openMetadataAt(d *fdEntry, name string, follow bool) (*os.File, uint64) {
	flags := unix.O_PATH
	if !follow {
		flags |= unix.O_NOFOLLOW
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
		atim = st.Atim.Sec*1e9 + st.Atim.Nsec
		ctim = st.Ctim.Sec*1e9 + st.Ctim.Nsec
	}
	return
}

func hostAccessTime(info os.FileInfo) time.Time {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Atim.Sec, st.Atim.Nsec)
	}
	return info.ModTime()
}

func setFileTimes(file *os.File, times []time.Time) error {
	values := []unix.Timespec{unix.NsecToTimespec(times[0].UnixNano()), unix.NsecToTimespec(times[1].UnixNano())}
	return unix.UtimesNanoAt(int(file.Fd()), "", values, unix.AT_EMPTY_PATH)
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
	return unix.Renameat(int(oldParent.Fd()), oldName, int(newParent.Fd()), newName)
}

func symlinkAt(target string, parent *os.File, name string) error {
	return unix.Symlinkat(target, int(parent.Fd()), name)
}

func linkAtFollow(oldDirectory *fdEntry, oldName string, newParent *os.File, newLeaf string) uint64 {
	// Preview 1 permits implementations to reject link-time symlink following.
	// Refusing it avoids Linux AT_EMPTY_PATH's CAP_DAC_READ_SEARCH requirement
	// and the unsafe /proc/self/fd fallback on hosts without procfs.
	return wasiEInval
}

func allocateFile(file *os.File, offset, length int64) error {
	return unix.Fallocate(int(file.Fd()), 0, offset, length)
}
