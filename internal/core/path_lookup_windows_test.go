//go:build windows

package core

import (
	"os"
	"testing"

	"github.com/wago-org/wasi/internal/winfs"
	"golang.org/x/sys/windows"
)

func requirePathLookupSymlinkControl(t *testing.T, e *Plugin, name string) {
	t.Helper()
	h, err := winfs.OpenAt(windows.Handle(e.fs.fds[3].file.Fd()), name, os.O_RDONLY, 0, true, false, false)
	if err != nil {
		t.Skipf("native directory symlink unsupported: %v", err)
	}
	file := os.NewFile(uintptr(h), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.IsDir() {
		t.Skipf("native directory symlink unsupported: %v", err)
	}
}
