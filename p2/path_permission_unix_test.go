//go:build darwin || linux

package p2

import (
	"errors"
	"os"
	"testing"
)

func TestOpenUnderWalkPreservesTargetPermissionError(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("file", root+"/link"); err != nil {
		t.Fatal(err)
	}
	base, err := openPreopenDirectory(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	readlink := hostFS.Readlinkat
	hostFS.Readlinkat = func(int, string, []byte) (int, error) { return 0, hostFS.EPERM }
	t.Cleanup(func() { hostFS.Readlinkat = readlink })
	f, err := openUnderWalk(base, "link", hostFS.O_RDONLY, 0, true)
	if f != nil {
		f.Close()
	}
	if !errors.Is(err, hostFS.EPERM) {
		t.Fatalf("target permission error = %v, want EPERM", err)
	}
}
