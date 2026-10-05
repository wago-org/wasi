//go:build windows

package p2

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsDirectoryStreamsEnumerateIndependently(t *testing.T) {
	root := t.TempDir()
	const entries = 1024
	// Each name alone takes over 200 bytes in the native directory record,
	// forcing several refills of Go's 64 KiB ReadDir buffer.
	for i := 0; i < entries; i++ {
		name := fmt.Sprintf("%04d-%s", i, strings.Repeat("x", 100))
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := openPreopenDirectory(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	first, err := newDirectoryStreamFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := newDirectoryStreamFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := dir.Close(); err != nil {
		t.Fatal(err)
	}
	seen := [2]map[string]bool{{}, {}}
	done := [2]bool{}
	for !done[0] || !done[1] {
		for i, stream := range []*os.File{first, second} {
			if done[i] {
				continue
			}
			batch, err := stream.ReadDir(1)
			if err == io.EOF {
				done[i] = true
				continue
			}
			if err != nil || len(batch) != 1 {
				t.Fatalf("stream %d entry = %v, %v", i, batch, err)
			}
			name := batch[0].Name()
			if seen[i][name] {
				t.Fatalf("stream %d repeated entry %q", i, name)
			}
			seen[i][name] = true
		}
	}
	for i := range seen {
		if len(seen[i]) != entries {
			t.Fatalf("stream %d entries = %d, want %d", i, len(seen[i]), entries)
		}
	}
}

func TestWindowsDirectoryStreamUsesPinnedDirectory(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "mounted")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inside"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dir, err := openPreopenDirectory(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err := os.Rename(root, filepath.Join(parent, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "replacement"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	stream, err := newDirectoryStreamFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := dir.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := stream.ReadDir(-1)
	if err != nil || len(entries) != 1 || entries[0].Name() != "inside" {
		t.Fatalf("pinned directory entries = %v, %v; want inside", entries, err)
	}
}
