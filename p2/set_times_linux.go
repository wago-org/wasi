//go:build linux

package p2

import (
	"errors"
	"os"
	"time"

	component "github.com/wago-org/component-model"
	"golang.org/x/sys/unix"
)

func platformSetTimesUnderPathFlags(dir *os.File, name string, follow bool, access, modification component.Value, now func() time.Time) error {
	return setTimesUnderPathFlagsLinux(dir, name, follow, access, modification, now, setMetadataTimes)
}

// updateTimes is passed explicitly so compatibility tests can simulate a kernel
// without AT_EMPTY_PATH without replacing a shared syscall or opening data.
func setTimesUnderPathFlagsLinux(dir *os.File, name string, follow bool, access, modification component.Value, now func() time.Time, updateTimes func(*os.File, time.Time, time.Time) error) error {
	f, err := platformOpenUnderPathFlags(dir, name, metadataOpenFlags|hostFS.O_WRITE_ATTRIBUTES, 0, follow)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	at, mt, changed, err := requestedTimes(access, modification, info, now)
	if err == nil && changed {
		err = updateTimes(f, at, mt)
		if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOSYS) {
			// AT_EMPTY_PATH was added in Linux 5.8. Older kernels can still
			// update the resolved leaf relative to its pinned parent without
			// opening a data stream or following the final link in the kernel.
			return setTimesUnderPathFlagsLinuxFallback(dir, name, follow, at, mt)
		}
	}
	return err
}

func setTimesUnderPathFlagsLinuxFallback(dir *os.File, name string, follow bool, atime, mtime time.Time) error {
	at, err := unix.TimeToTimespec(atime)
	if err != nil {
		return hostFS.EOVERFLOW
	}
	mt, err := unix.TimeToTimespec(mtime)
	if err != nil {
		return hostFS.EOVERFLOW
	}
	times := [2]unix.Timespec{at, mt}
	_, err = walkUnder(dir, name, 0, 0, follow, func(parent *os.File, leaf string) (os.FileInfo, error) {
		info, err := statMetadataLeaf(parent, leaf)
		if err != nil || follow && info.Mode()&os.ModeSymlink != 0 {
			return info, err
		}
		return info, unix.UtimesNanoAt(int(parent.Fd()), leaf, times[:], unix.AT_SYMLINK_NOFOLLOW)
	})
	return err
}
