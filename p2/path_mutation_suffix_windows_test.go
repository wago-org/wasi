//go:build windows

package p2

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsMutationDirectorySuffixOnFile(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := openPreopenDirectory(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	for _, name := range []string{"file/", "file/.", "file/./", "file//."} {
		t.Run(name, func(t *testing.T) {
			parent, leaf, err := parentUnderRaw(base, name)
			if err == nil {
				defer parent.Close()
				err = platformUnlinkAt(parent, leaf, 0)
			}
			if got := fsError(err); got != fsErrNotDirectory {
				t.Fatalf("unlink-file-at(%q) = %v (%d), want not-directory", name, err, got)
			}
			if data, err := os.ReadFile(file); err != nil || string(data) != "keep" {
				t.Fatalf("file after unlink = %q, %v; want keep", data, err)
			}
		})
	}
}

func TestWindowsRenameDirectoryWithTrailingSlash(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "source"), 0o700); err != nil {
		t.Fatal(err)
	}
	base, err := openPreopenDirectory(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	parent, leaf, err := parentUnderRaw(base, "source/")
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if err := platformRenameAt(parent, leaf, base, "target"); err != nil {
		t.Fatalf("rename-at(source/, target): %v", err)
	}
	if info, err := os.Stat(filepath.Join(root, "target")); err != nil || !info.IsDir() {
		t.Fatalf("renamed directory = %v, %v", info, err)
	}
}

func TestWindowsRenameTrailingDestination(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "missing"
		if existing {
			name = "existing"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "source"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "source", "data"), []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			if existing {
				if err := os.Mkdir(filepath.Join(root, "target"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			base, err := openPreopenDirectory(root, true)
			if err != nil {
				t.Fatal(err)
			}
			defer base.Close()
			from, source, err := parentUnderRaw(base, "source")
			if err != nil {
				t.Fatal(err)
			}
			defer from.Close()
			to, target, err := parentUnderRaw(base, "target/")
			if err != nil {
				t.Fatal(err)
			}
			defer to.Close()
			err = platformRenameAt(from, source, to, target)
			if existing {
				if err == nil {
					t.Fatal("trailing-destination rename replaced existing directory")
				}
				if data, err := os.ReadFile(filepath.Join(root, "source", "data")); err != nil || string(data) != "keep" {
					t.Fatalf("source changed: %q, %v", data, err)
				}
			} else {
				if err != nil {
					t.Fatalf("rename to missing target/: %v", err)
				}
				if data, err := os.ReadFile(filepath.Join(root, "target", "data")); err != nil || string(data) != "keep" {
					t.Fatalf("target data = %q, %v; want keep", data, err)
				}
			}
		})
	}
}

func TestWindowsUnlinkDirectorySuffixChecksPinnedLeaf(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "entry")
	if err := os.Mkdir(entry, 0o700); err != nil {
		t.Fatal(err)
	}
	base, err := openPreopenDirectory(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	parent, leaf, err := parentUnderRaw(base, "entry/")
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := platformUnlinkAt(parent, leaf, 0); fsError(err) != fsErrNotDirectory {
		t.Fatalf("unlink-file-at(entry/) after replacement = %v, want not-directory", err)
	}
	if data, err := os.ReadFile(entry); err != nil || string(data) != "keep" {
		t.Fatalf("replaced file after unlink = %q, %v; want keep", data, err)
	}
}

func TestWindowsCreateDirectoryWithTrailingSlash(t *testing.T) {
	root := t.TempDir()
	base, err := openPreopenDirectory(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	parent, leaf, err := parentUnder(base, "new/")
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if err := hostFS.Mkdirat(int(parent.Fd()), leaf, 0o700); err != nil {
		t.Fatalf("create-directory-at(new/): %v", err)
	}
	if info, err := os.Stat(filepath.Join(root, "new")); err != nil || !info.IsDir() {
		t.Fatalf("created directory = %v, %v", info, err)
	}
}
