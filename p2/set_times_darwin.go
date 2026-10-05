//go:build darwin

package p2

import (
	"os"
	"time"

	component "github.com/wago-org/component-model"
	"golang.org/x/sys/unix"
)

func platformSetTimesUnderPathFlags(dir *os.File, name string, follow bool, access, modification component.Value, now func() time.Time) error {
	_, err := walkUnder(dir, name, 0, 0, follow, func(parent *os.File, leaf string) (os.FileInfo, error) {
		info, err := statMetadataLeaf(parent, leaf)
		if err != nil || follow && info.Mode()&os.ModeSymlink != 0 {
			return info, err
		}
		at, mt, changed, err := requestedTimes(access, modification, info, now)
		if err != nil || !changed {
			return info, err
		}
		atime, err := unix.TimeToTimespec(at)
		if err != nil {
			return nil, hostFS.EOVERFLOW
		}
		mtime, err := unix.TimeToTimespec(mt)
		if err != nil {
			return nil, hostFS.EOVERFLOW
		}
		times := [2]unix.Timespec{atime, mtime}
		// The walker has resolved the target through the pinned parent. Never
		// follow this leaf again when applying the timestamp mutation.
		return info, unix.UtimesNanoAt(int(parent.Fd()), leaf, times[:], unix.AT_SYMLINK_NOFOLLOW)
	})
	return err
}
