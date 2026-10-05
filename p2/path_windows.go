//go:build windows

package p2

import (
	"path/filepath"
	"strings"

	"github.com/wago-org/wasi/internal/winfs"
	"golang.org/x/sys/windows"
)

func readlinkTargetAt(fd int, name string, buf []byte) (int, error) {
	target, relative, err := winfs.ReadlinkTargetAt(windows.Handle(fd), name)
	if err != nil {
		return 0, err
	}
	if !relative {
		return 0, hostFS.EPERM
	}
	target, err = normalizeSymlinkPath(target)
	if err != nil {
		return 0, err
	}
	return copy(buf, target), nil
}

func normalizeSymlinkPath(target string) (string, error) {
	if filepath.IsAbs(target) || filepath.VolumeName(target) != "" || strings.HasPrefix(target, `\`) || strings.HasPrefix(target, "/") {
		return "", hostFS.EPERM
	}
	return filepath.ToSlash(target), nil
}
