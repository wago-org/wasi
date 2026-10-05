//go:build windows

package winfs

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestDeleteAtDirectoryRequiredChecksPinnedLeafType(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	parent, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	h := windows.Handle(parent.Fd())
	if err := DeleteAtDirectoryRequired(h, "file", false); !errors.Is(err, windows.ERROR_DIRECTORY) {
		t.Fatalf("unlink file with directory requirement = %v, want ENOTDIR", err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "retained" {
		t.Fatalf("file changed after failed unlink = %q, %v", data, err)
	}
	if err := DeleteAtDirectoryRequired(h, "dir", false); !errors.Is(err, syscall.EISDIR) {
		t.Fatalf("unlink directory with directory requirement = %v, want EISDIR", err)
	}
	if err := DeleteAtDirectoryRequired(h, "dir", true); err != nil {
		t.Fatalf("remove directory with directory requirement: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "dir")); !os.IsNotExist(err) {
		t.Fatalf("directory remains after removal: %v", err)
	}
}
