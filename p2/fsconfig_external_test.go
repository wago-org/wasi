package p2_test

import (
	"testing"

	"github.com/wago-org/wasi/p2"
)

// This compile-time API test intentionally lives outside package p2. It
// verifies embedders can grant a real preopen without naming internal types.
func TestExternalCallerCanConfigureDirectoryPreopen(t *testing.T) {
	fs := p2.NewFSConfig().WithDirMount(t.TempDir(), "/work")
	var _ p2.FSConfig = fs
	_ = p2.Config{FS: fs}
}
