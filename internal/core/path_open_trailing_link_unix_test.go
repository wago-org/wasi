//go:build linux || darwin

package core

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestPathOpenTrailingSlashFollowsDirectoryLink(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("directory", filepath.Join(root, "link")); err != nil {
		t.Skipf("native symlink unavailable: %v", err)
	}
	e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true}}})
	defer e.closeAll()
	requirePathLookupSymlinkControl(t, e, "link")

	name := "link/"
	mem := make([]byte, len(name)+4)
	copy(mem, name)
	var result [1]uint64
	e.pathOpen(testModule{mem}, []uint64{3, 0, 0, uint64(len(name)), 2, rightFDRead, 0, 0, uint64(len(name))}, result[:])
	if result[0] != wasiOK {
		t.Fatalf("path_open %q without SYMLINK_FOLLOW = %d, want success", name, result[0])
	}
	fd := binary.LittleEndian.Uint32(mem[len(name):])
	defer e.fdClose(testModule{mem}, []uint64{uint64(fd)}, result[:])
	if info, err := e.fs.fds[fd].file.Stat(); err != nil || !info.IsDir() {
		t.Fatalf("opened path is not a directory: %v, %v", info, err)
	}
}
