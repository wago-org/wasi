//go:build linux

package p2

import (
	"io/fs"
	"os"
	"syscall"
	"time"

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
	times := []sysunix.Timeval{sysunix.NsecToTimeval(atime.UnixNano()), sysunix.NsecToTimeval(mtime.UnixNano())}
	return sysunix.Futimes(int(f.Fd()), times)
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
