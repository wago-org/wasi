//go:build windows

package p2

import (
	"io/fs"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func hostStat(info fs.FileInfo) (nlink uint64, atime, mtime, ctime time.Time, dev, ino uint64) {
	mtime = info.ModTime()
	if st, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return 1, time.Unix(0, st.LastAccessTime.Nanoseconds()), mtime,
			time.Unix(0, st.CreationTime.Nanoseconds()), 0, 0
	}
	return 1, mtime, mtime, mtime, 0, 0
}

func setFileTimes(f *os.File, atime, mtime time.Time) error {
	at := windows.NsecToFiletime(atime.UnixNano())
	mt := windows.NsecToFiletime(mtime.UnixNano())
	return windows.SetFileTime(windows.Handle(f.Fd()), nil, &at, &mt)
}

func syncFileData(f *os.File) error { return f.Sync() }
