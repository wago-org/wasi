//go:build linux || darwin

package p2

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUnlinkFileAtPreservesDirectorySuffix(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	base, err := openPreopenDirectory(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()

	for _, name := range []string{"file/", "file/.", "file/./", "file//."} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(file, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			parent, leaf, err := parentUnder(base, name)
			if err != nil {
				t.Fatal(err)
			}
			defer parent.Close()
			if err := hostFS.Unlinkat(int(parent.Fd()), leaf, 0); !errors.Is(err, hostFS.ENOTDIR) {
				t.Fatalf("unlink-file-at(%q) = %v, want ENOTDIR", name, err)
			}
			if data, err := os.ReadFile(file); err != nil || string(data) != "keep" {
				t.Fatalf("file after unlink = %q, %v; want keep", data, err)
			}
		})
	}
}
