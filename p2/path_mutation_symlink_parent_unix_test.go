//go:build linux || darwin

package p2

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMutationParentAllowsSymlinkStepsBackToBase(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "victim"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := openPreopenDirectory(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	if err := hostFS.Symlinkat("a/b", int(base.Fd()), "shortcut"); err != nil {
		t.Fatal(err)
	}
	if escaped, _, err := parentUnder(base, "shortcut/../../../victim"); !errors.Is(err, hostFS.EPERM) {
		if escaped != nil {
			escaped.Close()
		}
		t.Fatalf("path stepping outside base = %v, want EPERM", err)
	}
	parent, leaf, err := parentUnder(base, "shortcut/../../victim")
	if err != nil {
		t.Fatalf("confined symlink and parent steps: %v", err)
	}
	defer parent.Close()
	if err := hostFS.Unlinkat(int(parent.Fd()), leaf, 0); err != nil {
		t.Fatalf("unlink through confined symlink: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "victim")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("victim remains after unlink: %v", err)
	}
}
