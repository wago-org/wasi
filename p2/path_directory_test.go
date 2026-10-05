package p2

import (
	"os"
	"testing"
)

func TestOpenUnderWalkPreservesBaseDirectoryName(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(root+"/directory", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root+"/entry", []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := openPreopenDirectory(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	for _, name := range []string{".", "./", "directory/.."} {
		t.Run(name, func(t *testing.T) {
			f, err := openUnderWalk(base, name, hostFS.O_RDONLY|hostFS.O_DIRECTORY, 0, false)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			entries, err := f.ReadDir(-1)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, entry := range entries {
				info, err := entry.Info()
				if err != nil {
					t.Fatalf("directory entry %q metadata: %v", entry.Name(), err)
				}
				if entry.Name() == "entry" {
					found = true
					if info.Size() != 4 {
						t.Fatalf("entry size = %d, want 4", info.Size())
					}
				}
			}
			if !found {
				t.Fatal("base directory entry missing")
			}
			if f.Name() != base.Name() {
				t.Fatalf("base directory name = %q, want %q", f.Name(), base.Name())
			}
		})
	}
}
