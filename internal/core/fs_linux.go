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

const secureResolve = unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS

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

func setFileTimes(file *os.File, times []unix.Timespec) error {
	return unix.UtimesNanoAt(int(file.Fd()), "", times, unix.AT_EMPTY_PATH)
}

func setPathTimes(parent *os.File, leaf string, times []unix.Timespec, noFollow bool) error {
	flags := 0
	if noFollow {
		flags = unix.AT_SYMLINK_NOFOLLOW
	}
	return unix.UtimesNanoAt(int(parent.Fd()), leaf, times, flags)
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
