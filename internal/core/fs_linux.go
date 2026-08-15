//go:build linux

package core

import (
	"os"
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
	oldFile, code := openMetadataAt(oldDirectory, oldName, true)
	if code != 0 {
		return code
	}
	defer oldFile.Close()
	return errno(unix.Linkat(int(oldFile.Fd()), "", int(newParent.Fd()), newLeaf, unix.AT_EMPTY_PATH))
}
