//go:build windows

package p2

import (
	"io/fs"
	"math"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

const metadataHandleFlag = 1 << 27
const metadataOpenFlags = metadataHandleFlag

func hostStat(info fs.FileInfo) (nlink uint64, atime, mtime, ctime time.Time, dev, ino uint64) {
	mtime = info.ModTime()
	if st, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return 1, filetimeTime(st.LastAccessTime), filetimeTime(st.LastWriteTime),
			filetimeTime(st.CreationTime), 0, 0
	}
	return 1, mtime, mtime, mtime, 0, 0
}

func setFileTimes(f *os.File, atime, mtime time.Time) error {
	at, err := timeFiletime(atime)
	if err != nil {
		return err
	}
	mt, err := timeFiletime(mtime)
	if err != nil {
		return err
	}
	return windows.SetFileTime(windows.Handle(f.Fd()), nil, &at, &mt)
}

const filetimeEpochSeconds = 11644473600
const filetimeTicksPerSecond = 10000000

func timeFiletime(value time.Time) (windows.Filetime, error) {
	seconds := value.Unix()
	// Windows uses positive signed 64-bit ticks since 1601; zero and -1
	// are SetFileTime control values. Check before adding or multiplying.
	if seconds < -filetimeEpochSeconds || seconds > math.MaxInt64/filetimeTicksPerSecond-filetimeEpochSeconds {
		return windows.Filetime{}, hostFS.EOVERFLOW
	}
	ticks := uint64(seconds+filetimeEpochSeconds)*filetimeTicksPerSecond + uint64(value.Nanosecond()/100)
	if ticks == 0 || ticks > math.MaxInt64 {
		return windows.Filetime{}, hostFS.EOVERFLOW
	}
	return windows.Filetime{LowDateTime: uint32(ticks), HighDateTime: uint32(ticks >> 32)}, nil
}

func filetimeTime(value syscall.Filetime) time.Time {
	ticks := uint64(value.HighDateTime)<<32 | uint64(value.LowDateTime)
	return time.Unix(int64(ticks/filetimeTicksPerSecond)-filetimeEpochSeconds,
		int64(ticks%filetimeTicksPerSecond)*100)
}

func setMetadataTimes(f *os.File, atime, mtime time.Time) error {
	return setFileTimes(f, atime, mtime)
}

func syncFileData(f *os.File) error { return f.Sync() }
