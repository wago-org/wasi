//go:build windows

package p2

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsRelativeDirectoryMutation(t *testing.T) {
	base, err := openPreopenDirectory(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	want := time.Date(2003, time.April, 5, 6, 7, 8, 0, time.UTC)
	if err := setFileTimes(base, want, want); err != nil {
		t.Fatalf("set preopen times: %v", err)
	}
	info, err := base.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(want) {
		t.Fatalf("preopen mtime = %v, want %v", info.ModTime(), want)
	}
	fd, err := hostFS.Openat(int(base.Fd()), ".", hostFS.O_RDONLY|hostFS.O_DIRECTORY|hostFS.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	parent := os.NewFile(uintptr(fd), base.Name())
	defer parent.Close()
	if err := hostFS.Symlinkat("", int(parent.Fd()), "empty-target"); !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("Symlinkat empty target = %T %v", err, err)
	}
	if err := hostFS.Mkdirat(int(parent.Fd()), "work", 0o755); err != nil {
		t.Fatalf("Mkdirat: %T %v", err, err)
	}
	if err := os.WriteFile(filepath.Join(base.Name(), "source"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := hostFS.Linkat(int(parent.Fd()), "source", int(parent.Fd()), "linked", 0); err != nil {
		t.Fatalf("Linkat: %T %v", err, err)
	}
	if err := hostFS.Renameat(int(parent.Fd()), "linked", int(parent.Fd()), "renamed"); err != nil {
		t.Fatalf("Renameat: %T %v", err, err)
	}
	if err := hostFS.Symlinkat("source", int(parent.Fd()), "symbolic"); err != nil {
		t.Fatalf("Symlinkat: %T %v", err, err)
	}
	if err := hostFS.Symlinkat("work", int(parent.Fd()), "symbolic-dir"); err != nil {
		t.Fatalf("Symlinkat directory: %T %v", err, err)
	}
	if info, err := os.Stat(filepath.Join(base.Name(), "symbolic-dir")); err != nil || !info.IsDir() {
		t.Fatalf("directory symlink stat = %#v, %v", info, err)
	}
	if err := hostFS.Symlinkat("work/source", int(parent.Fd()), "slash-target"); err != nil {
		t.Fatalf("Symlinkat slash target: %T %v", err, err)
	}
	buf := make([]byte, 64)
	n, err := hostFS.Readlinkat(int(parent.Fd()), "symbolic", buf)
	if err != nil || string(buf[:n]) != "source" {
		t.Fatalf("Readlinkat = %q, %v", buf[:n], err)
	}
	n, err = hostFS.Readlinkat(int(parent.Fd()), "slash-target", buf)
	if err != nil || string(buf[:n]) != "work/source" {
		t.Fatalf("Readlinkat slash target = %q, %v", buf[:n], err)
	}
	if _, err := hostFS.Openat(int(parent.Fd()), ".", hostFS.O_RDWR|hostFS.O_DIRECTORY, 0); !errors.Is(err, syscall.EISDIR) {
		t.Fatalf("Openat writable dot = %T %v", err, err)
	}
	if err := hostFS.Unlinkat(int(parent.Fd()), "renamed", 0); err != nil {
		t.Fatalf("Unlinkat file: %T %v", err, err)
	}
	if err := hostFS.Unlinkat(int(parent.Fd()), "symbolic-dir", 0); err != nil {
		t.Fatalf("Unlinkat directory symlink: %T %v", err, err)
	}
	if err := hostFS.Unlinkat(int(parent.Fd()), "work", hostFS.AT_REMOVEDIR); err != nil {
		t.Fatalf("Unlinkat directory: %T %v", err, err)
	}
}

func TestWindowsFilesystemErrorMapping(t *testing.T) {
	tests := map[error]uint32{
		windows.ERROR_ACCESS_DENIED:     fsErrAccess,
		windows.ERROR_DIR_NOT_EMPTY:     fsErrNotEmpty,
		windows.ERROR_DISK_FULL:         fsErrInsufficientSpace,
		windows.ERROR_NOT_SAME_DEVICE:   fsErrCrossDevice,
		windows.ERROR_SHARING_VIOLATION: fsErrBusy,
	}
	for input, want := range tests {
		if got := fsError(input); got != want {
			t.Errorf("fsError(%v) = %d, want %d", input, got, want)
		}
	}
}
