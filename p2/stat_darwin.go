//go:build darwin

package p2

import (
	"io/fs"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func hostStat(info fs.FileInfo) (nlink uint64, atime, mtime, ctime time.Time, dev, ino uint64) {
	mtime = info.ModTime()
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink), time.Unix(st.Atimespec.Sec, st.Atimespec.Nsec), mtime,
			time.Unix(st.Ctimespec.Sec, st.Ctimespec.Nsec), uint64(st.Dev), st.Ino
	}
	return 1, mtime, mtime, mtime, 0, 0
}

func setFileTimes(f *os.File, atime, mtime time.Time) error {
	times := []unix.Timeval{unix.NsecToTimeval(atime.UnixNano()), unix.NsecToTimeval(mtime.UnixNano())}
	return unix.Futimes(int(f.Fd()), times)
}
func syncFileData(f *os.File) error { return unix.Fsync(int(f.Fd())) }
