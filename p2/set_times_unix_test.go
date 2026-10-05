//go:build linux || darwin

package p2

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSetTimesUnderPathFlagsOnUnreadableOwnedFile(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(file, 0o600)
	base, err := openPreopenDirectory(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	want := time.Date(2008, time.September, 10, 11, 12, 13, 123456789, time.UTC)
	if err := setTimesUnderPathFlags(base, "file", 0, timestampForTest(want), timestampForTest(want), time.Now); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil || !info.ModTime().Equal(want) {
		t.Fatalf("owned unreadable file mtime = %v, %v", info, err)
	}
}

func TestSetTimesUnderPathFlagsOnUnixSocket(t *testing.T) {
	// Keep the pathname within Darwin's Unix-domain socket length limit.
	root, err := os.MkdirTemp("", "wasi-time-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	socket := filepath.Join(root, "socket")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	base, err := openPreopenDirectory(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	want := time.Date(2009, time.October, 11, 12, 13, 14, 0, time.UTC)
	if err := setTimesUnderPathFlags(base, "socket", 0, timestampForTest(want), timestampForTest(want), time.Now); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 || !info.ModTime().Equal(want) {
		t.Fatalf("Unix socket mtime = %v, %v", info, err)
	}
}
