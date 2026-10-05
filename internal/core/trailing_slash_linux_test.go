//go:build linux

package core

import (
	"os"
	"testing"
)

func TestMetadataPathsPreserveTrailingSlashSemantics(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(root+"/file", []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root+"/directory", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("directory", root+"/dirlink"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", root+"/filelink"); err != nil {
		t.Fatal(err)
	}
	e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true}}})
	defer e.closeAll()
	mem := make([]byte, 512)
	result := make([]uint64, 1)

	copy(mem, "file/")
	e.pathFilestatGet(testModule{mem}, []uint64{3, 0, 0, 5, 200}, result)
	if result[0] != wasiENotdir {
		t.Fatalf("stat file/ = %d, want ENOTDIR", result[0])
	}

	copy(mem, "dirlink/")
	e.pathFilestatGet(testModule{mem}, []uint64{3, 0, 0, 8, 200}, result)
	if result[0] != wasiOK || mem[216] != filetypeDirectory {
		t.Fatalf("stat dirlink/ = errno %d type %d, want directory", result[0], mem[216])
	}

	copy(mem, "filelink/")
	e.pathReadlink(testModule{mem}, []uint64{3, 0, 9, 300, 100, 400}, result)
	if result[0] != wasiENotdir {
		t.Fatalf("readlink filelink/ = %d, want ENOTDIR", result[0])
	}

	before, err := os.Stat(root + "/file")
	if err != nil {
		t.Fatal(err)
	}
	copy(mem, "file/")
	e.pathFilestatSetTimes(testModule{mem}, []uint64{3, 1, 0, 5, 0, 1_000_000_000, 4}, result)
	if result[0] != wasiENotdir {
		t.Fatalf("set-times file/ = %d, want ENOTDIR", result[0])
	}
	after, err := os.Stat(root + "/file")
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("set-times file/ changed the regular file")
	}
}
