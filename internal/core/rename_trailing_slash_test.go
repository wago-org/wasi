package core

import (
	"os"
	"path/filepath"
	"testing"
)

func callRenameForTest(e *Plugin, oldFD uint32, oldName string, newFD uint32, newName string) uint64 {
	mem := make([]byte, len(oldName)+len(newName))
	copy(mem, oldName)
	copy(mem[len(oldName):], newName)
	var result [1]uint64
	e.pathRename(testModule{mem}, []uint64{uint64(oldFD), 0, uint64(len(oldName)), uint64(newFD), uint64(len(oldName)), uint64(len(newName))}, result[:])
	return result[0]
}

func TestPathRenameTrailingSlashRejectsRegularFiles(t *testing.T) {
	for _, tc := range []struct{ name, oldName, newName, destination string }{
		{"source", "source/", "renamed", "missing"},
		{"destination missing", "source", "renamed/", "missing"},
		{"destination file", "source", "renamed/", "file"},
		{"destination directory", "source", "renamed/", "directory"},
		{"both", "source///", "renamed///", "file"},
		{"cleaned source", "source/./", "renamed", "missing"},
		{"cleaned destination", "source", "renamed/./", "file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "source"), []byte("source data"), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.destination == "file" {
				if err := os.WriteFile(filepath.Join(root, "renamed"), []byte("destination data"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if tc.destination == "directory" {
				if err := os.Mkdir(filepath.Join(root, "renamed"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true, MutateDirectory: true}}})
			defer e.closeAll()
			if code := callRenameForTest(e, 3, tc.oldName, 3, tc.newName); code != wasiENotdir {
				t.Errorf("rename %q -> %q = %d, want ENOTDIR", tc.oldName, tc.newName, code)
			}
			if contents, err := os.ReadFile(filepath.Join(root, "source")); err != nil || string(contents) != "source data" {
				t.Fatalf("failed rename changed source: %q, %v", contents, err)
			}
			if tc.destination == "file" {
				if contents, err := os.ReadFile(filepath.Join(root, "renamed")); err != nil || string(contents) != "destination data" {
					t.Fatalf("failed rename changed destination: %q, %v", contents, err)
				}
			} else if tc.destination == "directory" {
				if info, err := os.Stat(filepath.Join(root, "renamed")); err != nil || !info.IsDir() {
					t.Fatalf("failed rename changed destination directory: %v, %v", info, err)
				}
			} else if _, err := os.Lstat(filepath.Join(root, "renamed")); !os.IsNotExist(err) {
				t.Fatalf("failed rename created destination: %v", err)
			}
		})
	}
}

func TestPathRenameTrailingSlashAllowsDirectories(t *testing.T) {
	for _, tc := range []struct{ oldName, newName string }{
		{"source/", "renamed"}, {"source", "renamed/"}, {"source///", "renamed///"},
	} {
		t.Run(tc.oldName+" -> "+tc.newName, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "source", "child"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "source", "child", "data"), []byte("retained"), 0o600); err != nil {
				t.Fatal(err)
			}
			e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true, MutateDirectory: true}}})
			defer e.closeAll()
			if code := callRenameForTest(e, 3, tc.oldName, 3, tc.newName); code != wasiOK {
				t.Fatalf("directory rename = %d", code)
			}
			if data, err := os.ReadFile(filepath.Join(root, "renamed", "child", "data")); err != nil || string(data) != "retained" {
				t.Fatalf("renamed directory content = %q, %v", data, err)
			}
			if _, err := os.Lstat(filepath.Join(root, "source")); !os.IsNotExist(err) {
				t.Fatalf("source remains after rename: %v", err)
			}
		})
	}
}

func TestPathRenameDotAndParentLeafDoNotMoveDirectory(t *testing.T) {
	for _, tc := range []struct{ oldName, newName string }{
		{"source/./", "renamed/./"},
		{"source/child/../", "renamed/"},
	} {
		t.Run(tc.oldName+" -> "+tc.newName, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "source", "child"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "source", "child", "data"), []byte("retained"), 0o600); err != nil {
				t.Fatal(err)
			}
			e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true, MutateDirectory: true}}})
			defer e.closeAll()
			if code := callRenameForTest(e, 3, tc.oldName, 3, tc.newName); code == wasiOK {
				t.Fatalf("rename %q -> %q unexpectedly succeeded", tc.oldName, tc.newName)
			}
			if data, err := os.ReadFile(filepath.Join(root, "source", "child", "data")); err != nil || string(data) != "retained" {
				t.Fatalf("failed rename changed source: %q, %v", data, err)
			}
			if _, err := os.Lstat(filepath.Join(root, "renamed")); !os.IsNotExist(err) {
				t.Fatalf("failed rename created destination: %v", err)
			}
		})
	}
}

func TestPathRenameTrailingSlashRejectsNonDirectoryTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "source"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source", "data"), []byte("directory data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "renamed"), []byte("destination data"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true, MutateDirectory: true}}})
	defer e.closeAll()
	if code := callRenameForTest(e, 3, "source", 3, "renamed/"); code != wasiENotdir {
		t.Fatalf("directory -> regular destination/ = %d, want ENOTDIR", code)
	}
	if data, err := os.ReadFile(filepath.Join(root, "source", "data")); err != nil || string(data) != "directory data" {
		t.Fatalf("source directory changed: %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "renamed")); err != nil || string(data) != "destination data" {
		t.Fatalf("destination file changed: %q, %v", data, err)
	}
}

func TestPathRenameOrdinaryFileAcrossPinnedParents(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "old"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "new"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "old", "source"), []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true, MutateDirectory: true}}})
	defer e.closeAll()
	if code := callRenameForTest(e, 3, "old/source", 3, "new/renamed"); code != wasiOK {
		t.Fatalf("ordinary rename = %d", code)
	}
	if data, err := os.ReadFile(filepath.Join(root, "new", "renamed")); err != nil || string(data) != "retained" {
		t.Fatalf("ordinary rename lost contents: %q, %v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "old", "source")); !os.IsNotExist(err) {
		t.Fatalf("ordinary source remains: %v", err)
	}
}

func TestPathRenameTrailingSlashRejectsLeafLinks(t *testing.T) {
	for _, tc := range []struct {
		name, oldName, newName string
		sourceLink             bool
	}{
		{"source", "link/", "renamed", true},
		{"source requires directory destination", "link", "renamed/", true},
		{"destination", "source", "link/", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "directory", "data"), []byte("retained"), 0o600); err != nil {
				t.Fatal(err)
			}
			if !tc.sourceLink {
				if err := os.Mkdir(filepath.Join(root, "source"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink("directory", filepath.Join(root, "link")); err != nil {
				t.Skipf("host cannot create ordinary directory symlink: %v", err)
			}
			// Some Windows emulators report successful symlink creation without
			// creating a native link. Require a usable fixture before the rename.
			if info, err := os.Lstat(filepath.Join(root, "link")); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Skipf("host did not create a usable symlink: %v, %v", info, err)
			}
			e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true, MutateDirectory: true}}})
			defer e.closeAll()
			if code := callRenameForTest(e, 3, tc.oldName, 3, tc.newName); code != wasiENotdir && code != wasiELoop {
				t.Fatalf("rename leaf link = %d, want ENOTDIR or ELOOP", code)
			}
			if link, err := os.Readlink(filepath.Join(root, "link")); err != nil || link != "directory" {
				t.Fatalf("rename changed link: %q, %v", link, err)
			}
			if data, err := os.ReadFile(filepath.Join(root, "directory", "data")); err != nil || string(data) != "retained" {
				t.Fatalf("rename changed link target: %q, %v", data, err)
			}
			if !tc.sourceLink {
				if info, err := os.Stat(filepath.Join(root, "source")); err != nil || !info.IsDir() {
					t.Fatalf("rename changed source directory: %v, %v", info, err)
				}
			}
		})
	}
}

func TestPathRenameTrailingSlashPreservesErrorPrecedence(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := newTestPlugin(t, Config{Mounts: []Preopen{
		{GuestPath: "/a", HostPath: root, Read: true, Write: true, MutateDirectory: true},
		{GuestPath: "/b", HostPath: other, Read: true, Write: true, MutateDirectory: true},
	}})
	defer e.closeAll()
	for _, tc := range []struct {
		name, oldName, newName string
		oldFD, newFD           uint32
		want, removeRight      uint64
	}{
		{"source right", "source/", "renamed/", 3, 3, wasiENotcapable, rightPathRenameSource},
		{"destination right", "source/", "renamed/", 3, 3, wasiENotcapable, rightPathRenameTarget},
		{"cross mount", "source/", "renamed/", 3, 4, wasiEXdev, 0},
		{"bad source fd", "source/", "renamed/", 100, 3, wasiEBadf, 0},
		{"source escapes", "../source/", "renamed/", 3, 3, wasiENotcapable, 0},
		{"source missing", "missing/", "renamed/", 3, 3, wasiENoent, 0},
		{"destination parent missing", "source/", "missing/renamed/", 3, 3, wasiENoent, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := e.fs.fds[3].rights
			e.fs.fds[3].rights &^= tc.removeRight
			defer func() { e.fs.fds[3].rights = original }()
			if code := callRenameForTest(e, tc.oldFD, tc.oldName, tc.newFD, tc.newName); code != tc.want {
				t.Fatalf("rename errno = %d, want %d", code, tc.want)
			}
		})
	}
	if data, err := os.ReadFile(filepath.Join(root, "source")); err != nil || string(data) != "data" {
		t.Fatalf("error controls changed source: %q, %v", data, err)
	}
}

func BenchmarkPathRename(b *testing.B) {
	for _, trailing := range []bool{false, true} {
		name := "ordinary"
		if trailing {
			name = "trailing"
		}
		b.Run(name, func(b *testing.B) {
			root := b.TempDir()
			if err := os.Mkdir(filepath.Join(root, "source"), 0o700); err != nil {
				b.Fatal(err)
			}
			e := &Plugin{cfg: Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true, MutateDirectory: true}}}}
			e.resetFS()
			if err := e.initFS(false); err != nil {
				b.Fatal(err)
			}
			defer e.closeAll()
			oldName, newName := "source", "renamed"
			if trailing {
				oldName, newName = "source/", "renamed/"
			}
			mem := make([]byte, len(oldName)+len(newName))
			copy(mem, oldName)
			copy(mem[len(oldName):], newName)
			m := testModule{mem}
			args := []uint64{3, 0, uint64(len(oldName)), 3, uint64(len(oldName)), uint64(len(newName))}
			var result [1]uint64
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				e.pathRename(m, args, result[:])
				if result[0] != wasiOK {
					b.Fatal(result[0])
				}
				args[1], args[4] = args[4], args[1]
				args[2], args[5] = args[5], args[2]
			}
		})
	}
}
