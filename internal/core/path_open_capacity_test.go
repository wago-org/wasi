package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathOpenAtGuestDescriptorLimitDoesNotMutate(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "existing")
	if err := os.WriteFile(path, []byte("keep this data"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := newTestPlugin(t, Config{
		Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true, MutateDirectory: true}},
		MaxOpenFiles: 4, // stdio and this preopen consume all guest descriptors
	})
	defer e.closeAll()

	for _, tc := range []struct {
		name   string
		oflags uint64
	}{
		{"existing", 8}, // truncate
		{"created", 1},  // create
	} {
		t.Run(tc.name, func(t *testing.T) {
			mem := make([]byte, len(tc.name)+4)
			copy(mem, tc.name)
			result := []uint64{999}
			e.pathOpen(testModule{mem}, []uint64{3, 0, 0, uint64(len(tc.name)), tc.oflags, rightFDWrite, 0, 0, uint64(len(tc.name))}, result)
			if result[0] != wasiEMfile {
				t.Fatalf("path_open returned %d, want EMFILE", result[0])
			}
			if tc.oflags == 8 {
				if data, err := os.ReadFile(path); err != nil || string(data) != "keep this data" {
					t.Fatalf("failed open truncated existing file: %q, %v", data, err)
				}
			} else if _, err := os.Lstat(filepath.Join(root, "created")); !os.IsNotExist(err) {
				t.Fatalf("failed open created a file: %v", err)
			}
		})
	}
}
