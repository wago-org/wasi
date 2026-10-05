//go:build linux || darwin

package p2

import (
	"errors"
	"os"
	"testing"
)

func TestCreateAbsoluteSymlinkTargetIsRejected(t *testing.T) {
	root := t.TempDir()
	dir, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()

	if err := symlinkUnder(dir, "/outside", "created"); !errors.Is(err, hostFS.EPERM) {
		t.Fatalf("symlink with absolute target = %v, want EPERM", err)
	}
	if _, err := os.Lstat(root + "/created"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("absolute symlink was created: %v", err)
	}

}

func TestReadAbsoluteSymlinkTargetIsRejected(t *testing.T) {
	root := t.TempDir()
	dir, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err := os.Symlink("/outside", root+"/existing"); err != nil {
		t.Fatal(err)
	}
	if target, err := readlinkUnder(dir, "existing"); !errors.Is(err, hostFS.EPERM) {
		t.Fatalf("readlink absolute target = %q, %v; want EPERM", target, err)
	}
}

func TestRelativeSymlinkTargetRemainsReadable(t *testing.T) {
	root := t.TempDir()
	dir, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err := symlinkUnder(dir, "target", "link"); err != nil {
		t.Fatal(err)
	}
	if target, err := readlinkUnder(dir, "link"); err != nil || target != "target" {
		t.Fatalf("relative symlink = %q, %v; want target", target, err)
	}
}
