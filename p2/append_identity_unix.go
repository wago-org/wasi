//go:build linux || darwin

package p2

import (
	"io/fs"
	"os"
)

func appendTargetForFile(_ *os.File, info fs.FileInfo) (*appendTarget, error) {
	_, _, _, _, dev, ino := hostStat(info)
	return appendTargetForIdentity(dev, ino), nil
}
