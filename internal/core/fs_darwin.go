//go:build darwin

package core

import (
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// O_RESOLVE_BENEATH is available in the Darwin kernel and SDK but is absent
// from the x/sys version pinned by this module.
const (
	oResolveBeneath      = 0x00001000
	darwinENotcapableErr = syscall.Errno(107)
)

func openAt(d *fdEntry, name string, flags int, mode uint32) (*os.File, uint64) {
	if flags&unix.O_CREAT == 0 {
		mode = 0
	}
	fd, err := unix.Openat(int(d.file.Fd()), name, flags|unix.O_CLOEXEC|oResolveBeneath, mode)
	if err != nil {
		if err == darwinENotcapableErr || err == syscall.ELOOP && flags&unix.O_NOFOLLOW == 0 {
			return nil, wasiENotcapable
		}
		return nil, capabilityErr(err)
	}
	return os.NewFile(uintptr(fd), name), wasiOK
}

func openMetadataAt(d *fdEntry, name string, follow bool) (*os.File, uint64) {
	flags := unix.O_EVTONLY
	if !follow {
		flags |= unix.O_SYMLINK
	}
	return openAt(d, name, flags, 0)
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

func setFileTimes(file *os.File, times []unix.Timespec) error {
	timevals := []unix.Timeval{
		unix.NsecToTimeval(unix.TimespecToNsec(times[0])),
		unix.NsecToTimeval(unix.TimespecToNsec(times[1])),
	}
	return unix.Futimes(int(file.Fd()), timevals)
}

func setPathTimes(parent *os.File, leaf string, times []unix.Timespec, noFollow bool) error {
	flags := 0
	if noFollow {
		flags = unix.AT_SYMLINK_NOFOLLOW
	}
	return unix.UtimesNanoAt(int(parent.Fd()), leaf, times, flags)
}

func linkAtFollow(_ *fdEntry, _ string, _ *os.File, _ string) uint64 {
	// Darwin has no AT_EMPTY_PATH equivalent for linking an already resolved
	// descriptor. Reject rather than reintroduce a path race after validation.
	return wasiENotsup
}
