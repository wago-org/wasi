package p2

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNestedDirectoryEntriesHaveMetadata(t *testing.T) {
	testNestedDirectoryEntriesHaveMetadata(t, openUnder, false)
}

func TestPathFlagsNestedDirectoryEntriesHaveMetadata(t *testing.T) {
	testNestedDirectoryEntriesHaveMetadata(t, func(dir *os.File, name string, flags int, mode uint32) (*os.File, error) {
		return openUnderPathFlags(dir, name, flags, mode, 1)
	}, true)
}

func TestWalkNestedDirectoryEntriesHaveMetadata(t *testing.T) {
	testNestedDirectoryEntriesHaveMetadata(t, func(dir *os.File, name string, flags int, mode uint32) (*os.File, error) {
		return openUnderWalk(dir, name, flags, mode, true)
	}, true)
}

func testNestedDirectoryEntriesHaveMetadata(t *testing.T, open func(*os.File, string, int, uint32) (*os.File, error), links bool) {
	t.Helper()
	root := t.TempDir()
	nested := filepath.Join(root, "level-one", "level-two")
	if err := os.MkdirAll(filepath.Join(nested, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	const contents = "nested file contents"
	if err := os.WriteFile(filepath.Join(nested, "file.txt"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	var linkErr error
	if links {
		// Both paths remain inside this fixture. The parent step must occur
		// after following the link into level-one/level-two.
		base, err := os.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		linkErr = hostFS.Symlinkat("level-one/level-two", int(base.Fd()), "shortcut")
		base.Close()
		if linkErr != nil && runtime.GOOS != "windows" {
			t.Fatal(linkErr)
		}
		if err := os.Mkdir(filepath.Join(root, "level-two"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "level-two", "file.txt"), []byte("other"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		name   string
		paths  []string
		linked bool
	}{
		{"intermediate-directory", []string{"level-one/level-two"}, false},
		{"child-descriptor", []string{"level-one", "level-two"}, false},
		{"linked-parent-step", []string{"shortcut/../level-two"}, true},
	} {
		if tc.linked && !links {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			if tc.linked && linkErr != nil {
				t.Skipf("directory symlinks unavailable: %v", linkErr)
			}
			dir, err := os.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range tc.paths {
				child, openErr := open(dir, name, hostFS.O_RDONLY|hostFS.O_DIRECTORY, 0)
				dir.Close()
				if openErr != nil {
					t.Fatal(openErr)
				}
				dir = child
			}
			// read-directory duplicates the descriptor before reading entries.
			stream, err := dupFile(dir)
			dir.Close()
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			entries, err := stream.ReadDir(-1)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 2 {
				t.Fatalf("directory has %d entries, want 2", len(entries))
			}
			for _, entry := range entries {
				info, err := entry.Info()
				if err != nil {
					t.Errorf("metadata for nested entry %q: %v", entry.Name(), err)
					continue
				}
				switch entry.Name() {
				case "child":
					if !info.IsDir() || descriptorKind(info) != descriptorDirectory {
						t.Errorf("child metadata has mode %v, want directory", info.Mode())
					}
				case "file.txt":
					if descriptorKind(info) != descriptorRegularFile || info.Size() != int64(len(contents)) {
						t.Errorf("file metadata = mode %v, size %d; want regular file, size %d", info.Mode(), info.Size(), len(contents))
					}
				default:
					t.Errorf("unexpected entry %q", entry.Name())
				}
			}
		})
	}
}
