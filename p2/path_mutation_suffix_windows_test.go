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
			parent, leaf, err := parentUnder(base, name)
			if err == nil {
				defer parent.Close()
				err = hostFS.Unlinkat(int(parent.Fd()), leaf, 0)
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
	parent, leaf, err := parentUnder(base, "source/")
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if err := hostFS.Renameat(int(parent.Fd()), leaf, int(base.Fd()), "target"); err != nil {
		t.Fatalf("rename-at(source/, target): %v", err)
	}
	if info, err := os.Stat(filepath.Join(root, "target")); err != nil || !info.IsDir() {
		t.Fatalf("renamed directory = %v, %v", info, err)
	}
}
