//go:build linux || darwin

package core

import (
	"os"
	"path/filepath"
	"testing"
)

func requirePathLookupSymlinkControl(t *testing.T, e *Plugin, name string) {
	t.Helper()
	info, err := os.Stat(filepath.Join(e.fs.fds[3].file.Name(), name))
	if err != nil || !info.IsDir() {
		t.Skipf("native directory symlink unsupported: %v", err)
	}
}
