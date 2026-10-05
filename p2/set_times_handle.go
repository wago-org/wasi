//go:build windows

package p2

import (
	"os"
	"time"

	component "github.com/wago-org/component-model"
)

func platformSetTimesUnderPathFlags(dir *os.File, name string, follow bool, access, modification component.Value, now func() time.Time) error {
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
		err = setMetadataTimes(f, at, mt)
	}
	return err
}
