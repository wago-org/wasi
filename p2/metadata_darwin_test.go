//go:build darwin

package p2

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDarwinMetadataDoesNotRequireDataReadAccess(t *testing.T) {
	root := t.TempDir()
	name := filepath.Join(root, "unreadable")
	if err := os.WriteFile(name, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(name, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(name, 0o600)
	testDarwinMetadata(t, root, "unreadable", 0)
}

func TestDarwinMetadataCanStatUnixSocket(t *testing.T) {
	// Keep the socket path below Darwin's Unix-domain address limit.
	root, err := os.MkdirTemp("", "wasi-stat-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	listener, err := net.Listen("unix", filepath.Join(root, "socket"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	testDarwinMetadata(t, root, "socket", os.ModeSocket)
}

func testDarwinMetadata(t *testing.T, root, name string, mode os.FileMode) {
	t.Helper()
	base, err := openPreopenDirectory(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	want, err := os.Lstat(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	for _, flags := range []uint32{0, 1} {
		info, err := statUnderPathFlags(base, name, flags)
		if err != nil {
			t.Fatalf("metadata %q with flags %d: %v", name, flags, err)
		}
		if info.Mode().Type() != mode || info.Mode() != want.Mode() || info.Size() != want.Size() {
			t.Fatalf("metadata = %v, %d; want %v, %d", info.Mode(), info.Size(), want.Mode(), want.Size())
		}
		if !reflect.DeepEqual(statValue(info), statValue(want)) || !reflect.DeepEqual(metadataHash(info), metadataHash(want)) {
			t.Fatalf("metadata stat or hash differs from native lstat for %q", name)
		}
	}
}
