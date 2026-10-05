//go:build linux

package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathWalkFallbackFollowsConfinedFinalDirectoryLink(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("directory", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true}}})
	defer e.closeAll()
	file, code := openAtWalk(e.fs.fds[3], "link", hostOpenReadOnly|hostOpenDirectory, 0)
	if code != wasiOK {
		t.Fatalf("fallback open of directory link = %d, want success", code)
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil || !info.IsDir() {
		t.Fatalf("fallback opened non-directory: %v, %v", info, err)
	}
}

func TestPathWalkFallbackRejectsEscapingFinalLink(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("../outside", filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true}}})
	defer e.closeAll()
	file, code := openAtWalk(e.fs.fds[3], "escape", hostOpenReadOnly|hostOpenDirectory, 0)
	if file != nil {
		file.Close()
	}
	if code != wasiENotcapable {
		t.Fatalf("fallback open of escaping link = %d, want ENOTCAPABLE", code)
	}
}
