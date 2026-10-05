//go:build linux

package p2

import (
	"io/fs"
	"os"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	sysunix "golang.org/x/sys/unix"
)

const metadataHandleFlag = sysunix.O_PATH
const metadataOpenFlags = metadataHandleFlag

func hostStat(info fs.FileInfo) (nlink uint64, atime, mtime, ctime time.Time, dev, ino uint64) {
	mtime = info.ModTime()
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink), time.Unix(st.Atim.Sec, st.Atim.Nsec), mtime,
			time.Unix(st.Ctim.Sec, st.Ctim.Nsec), uint64(st.Dev), st.Ino
	}
	return 1, mtime, mtime, mtime, 0, 0
}

func setFileTimes(f *os.File, atime, mtime time.Time) error {
	at, err := sysunix.TimeToTimespec(atime)
	if err != nil {
		return hostFS.EOVERFLOW
	}
	mt, err := sysunix.TimeToTimespec(mtime)
	if err != nil {
		return hostFS.EOVERFLOW
	}
	times := [2]sysunix.Timespec{at, mt}
	// The kernel implements futimens as utimensat(fd, NULL, times, 0).
	// Unlike AT_EMPTY_PATH this ABI predates Linux 5.8, and it needs no
	// /proc pathname or nanoseconds-since-epoch conversion.
	_, _, errno := syscall.Syscall6(sysunix.SYS_UTIMENSAT, f.Fd(), 0,
		uintptr(unsafe.Pointer(&times[0])), 0, 0, 0)
	runtime.KeepAlive(f)
	if errno != 0 {
		return errno
	}
	return nil
}

func setMetadataTimes(f *os.File, atime, mtime time.Time) error {
	at, err := sysunix.TimeToTimespec(atime)
	if err != nil {
		return hostFS.EOVERFLOW
	}
	mt, err := sysunix.TimeToTimespec(mtime)
	if err != nil {
		return hostFS.EOVERFLOW
	}
	times := [2]sysunix.Timespec{at, mt}
	// AT_EMPTY_PATH updates the pinned O_PATH handle, including a symlink
	// itself. Futimes uses /proc/self/fd and can follow that link instead.
	return sysunix.UtimesNanoAt(int(f.Fd()), "", times[:], sysunix.AT_EMPTY_PATH|sysunix.AT_SYMLINK_NOFOLLOW)
}
func syncFileData(f *os.File) error { return sysunix.Fdatasync(int(f.Fd())) }
