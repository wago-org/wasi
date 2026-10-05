//go:build windows

package p2

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsRootedSymlinkTargetsAreRejected(t *testing.T) {
	dir, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	for _, target := range []string{"/outside", `\outside`, `C:\outside`, `C:outside`, `\\server\share\outside`} {
		t.Run(target, func(t *testing.T) {
			if err := symlinkUnder(dir, target, "created"); !errors.Is(err, hostFS.EPERM) {
				t.Fatalf("symlink target %q = %v, want EPERM", target, err)
			}
			if _, err := os.Lstat(filepath.Join(dir.Name(), "created")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rooted symlink was created: %v", err)
			}
		})
	}
}

func TestWindowsReadRootedSymlinkTargetIsRejected(t *testing.T) {
	dir, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err := hostFS.Symlinkat(`\outside`, int(dir.Fd()), "existing"); err != nil {
		if errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
			t.Skip("host does not support Windows symbolic links")
		}
		t.Fatal(err)
	}
	if target, err := readlinkUnder(dir, "existing"); !errors.Is(err, hostFS.EPERM) {
		t.Fatalf("readlink rooted target = %q, %v; want EPERM", target, err)
	}
}

func TestWindowsRelativeSymlinkTargetRemainsReadable(t *testing.T) {
	dir, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err := symlinkUnder(dir, `..\target`, "link"); err != nil {
		if errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
			t.Skip("host does not support Windows symbolic links")
		}
		t.Fatal(err)
	}
	if target, err := readlinkUnder(dir, "link"); err != nil || target != "../target" {
		t.Fatalf("relative symlink = %q, %v; want ../target", target, err)
	}
}

func TestWindowsReadlinkTargetValidation(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		relative bool
		want     string
		denied   bool
	}{
		{name: "non-relative flag", target: "target", denied: true},
		{name: "rooted substitute", target: `\target`, relative: true, denied: true},
		{name: "drive substitute", target: `C:\target`, relative: true, denied: true},
		{name: "drive relative substitute", target: `C:target`, relative: true, denied: true},
		{name: "relative substitute", target: `work\target`, relative: true, want: "work/target"},
		{name: "relative parent substitute", target: `..\target`, relative: true, want: "../target"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := windowsReadlinkTarget(test.target, test.relative)
			if test.denied {
				if got != "" || !errors.Is(err, hostFS.EPERM) {
					t.Fatalf("target = %q, %v; want EPERM", got, err)
				}
			} else if err != nil || got != test.want {
				t.Fatalf("target = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}
