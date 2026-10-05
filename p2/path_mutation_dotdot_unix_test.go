//go:build linux || darwin

package p2

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMutationParentResolvesComponentsBeforeDotDot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "regular"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := openPreopenDirectory(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()

	for _, tc := range []struct {
		path string
		want error
	}{
		{"missing/../victim", hostFS.ENOENT},
		{"regular/../victim", hostFS.ENOTDIR},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(root, "victim"), []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			parent, leaf, err := parentUnder(base, tc.path)
			if err == nil {
				defer parent.Close()
				err = hostFS.Unlinkat(int(parent.Fd()), leaf, 0)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("unlink-file-at(%q) = %v, want %v", tc.path, err, tc.want)
			}
			if data, err := os.ReadFile(filepath.Join(root, "victim")); err != nil || string(data) != "keep" {
				t.Fatalf("victim after unlink = %q, %v; want keep", data, err)
			}
		})
	}
}
