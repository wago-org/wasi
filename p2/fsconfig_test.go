package p2

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFSConfigDirMountIsImmutableAndNormalizesGuestPath(t *testing.T) {
	root := t.TempDir()
	base := NewFSConfig()
	mounted := base.WithDirMount(root, "/tmp/")

	if got := fsMountsFromConfig(base); len(got) != 0 {
		t.Fatalf("base config changed: %d mounts", len(got))
	}
	mounts := fsMountsFromConfig(mounted)
	if len(mounts) != 1 || mounts[0].guestPath != "/tmp" {
		t.Fatalf("mounts = %#v, want one mount at /tmp", mounts)
	}
	if _, errno := mounts[0].fs.OpenFile("../escape", 0, 0); errno == 0 {
		t.Fatal("mount allowed parent traversal")
	}
}

func TestFSConfigDirMountReadsAndWritesInsideRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "input"), []byte("in"), 0o600); err != nil {
		t.Fatal(err)
	}
	mount := fsMountsFromConfig(NewFSConfig().WithDirMount(root, "/"))[0]
	f, errno := mount.fs.OpenFile("input", 0, 0)
	if errno != 0 {
		t.Fatalf("open input: %v", errno)
	}
	defer f.Close()
	buf := make([]byte, 2)
	if n, errno := f.Read(buf); errno != 0 || n != 2 || string(buf) != "in" {
		t.Fatalf("read = %q, %d, %v", buf, n, errno)
	}
}

func TestFSConfigDirMountRejectsSymlinkEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	mount := fsMountsFromConfig(NewFSConfig().WithDirMount(root, "/"))[0]
	if _, errno := mount.fs.OpenFile("escape/secret", 0, 0); errno == 0 {
		t.Fatal("mount followed a symlink outside its host root")
	}
}
