//go:build windows

package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsRenameTrailingSlashExistingDirectoryFailsClosed(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "source"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source", "data"), []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "destination"), 0o700); err != nil {
		t.Fatal(err)
	}
	e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true, MutateDirectory: true}}})
	defer e.closeAll()
	if code := callRenameForTest(e, 3, "source", 3, "destination/"); code == wasiOK {
		t.Fatal("trailing-destination rename replaced an existing directory")
	}
	if data, err := os.ReadFile(filepath.Join(root, "source", "data")); err != nil || string(data) != "retained" {
		t.Fatalf("source changed: %q, %v", data, err)
	}
	if entries, err := os.ReadDir(filepath.Join(root, "destination")); err != nil || len(entries) != 0 {
		t.Fatalf("destination changed: %v, %v", entries, err)
	}
}

func TestWindowsRenameMixedTerminalSeparators(t *testing.T) {
	for _, tc := range []struct{ source, destination string }{
		{`source/\`, "target"},
		{`source\/`, "target"},
		{"source", `target/\`},
		{"source", `target\/`},
	} {
		t.Run(tc.source+"_to_"+tc.destination, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "source"), 0o700); err != nil {
				t.Fatal(err)
			}
			e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true, MutateDirectory: true}}})
			defer e.closeAll()
			if code := callRenameForTest(e, 3, tc.source, 3, tc.destination); code != wasiOK {
				t.Fatalf("rename %q -> %q = %d, want OK", tc.source, tc.destination, code)
			}
			if info, err := os.Stat(filepath.Join(root, "target")); err != nil || !info.IsDir() {
				t.Fatalf("target directory = %v, %v", info, err)
			}
		})
	}
}
