//go:build windows

package p2

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsStatAtReportsHardLinkCount(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "original")
	if err := os.WriteFile(original, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(original, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	base, err := openPreopenDirectory(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	info, err := statUnderPathFlags(base, "alias", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := statValue(info).([]any)[1]; got != uint64(2) {
		t.Fatalf("hard-link count = %v, want 2", got)
	}
}
