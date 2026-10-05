//go:build linux || darwin

package p2

import (
	"os"
	"path/filepath"
	"testing"

	component "github.com/wago-org/component-model"
	sysunix "golang.org/x/sys/unix"
)

func TestDirectoryEntryTypes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("directory", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := sysunix.Mkfifo(filepath.Join(root, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := openPreopenDirectory(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	file, err := newDirectoryStreamFile(base)
	if err != nil {
		t.Fatal(err)
	}
	stream := &directoryStream{file: file}
	defer closeDirectoryStream(stream)
	want := map[string]uint32{
		"file": descriptorRegularFile, "directory": descriptorDirectory,
		"link": descriptorSymbolicLink, "pipe": descriptorFIFO,
	}
	for range len(want) {
		result := stream.readEntry(255)[0].(component.ResultValue)
		if result.IsErr || result.Payload == nil {
			t.Fatalf("entry = %#v, want remaining types %v", result, want)
		}
		entry := result.Payload.([]component.Value)
		name := entry[1].(string)
		kind, exists := want[name]
		if !exists || entry[0] != kind {
			t.Fatalf("entry = %#v, want remaining types %v", entry, want)
		}
		delete(want, name)
	}
	result := stream.readEntry(255)[0].(component.ResultValue)
	if result.IsErr || result.Payload != nil {
		t.Fatalf("final entry = %#v, want EOF", result)
	}
}

// Known native entry types require directory read permission, not search.
func TestDirectoryEntryWithReadOnlyDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory search permission")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "entry"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	base, err := openPreopenDirectory(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	file, err := newDirectoryStreamFile(base)
	if err != nil {
		t.Fatal(err)
	}
	stream := &directoryStream{file: file}
	defer closeDirectoryStream(stream)
	reference, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reference.Close()
	if err := os.Chmod(root, 0400); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(root, 0700)
	// Establish the host known-type control on this filesystem first.
	entries, err := reference.ReadDir(1)
	if err != nil || len(entries) != 1 || entries[0].Type() != 0 {
		t.Skipf("filesystem lacks read-only known-file enumeration: %v, %v", entries, err)
	}
	var stat sysunix.Stat_t
	if err := sysunix.Fstatat(int(file.Fd()), "entry", &stat, sysunix.AT_SYMLINK_NOFOLLOW); err != sysunix.EACCES {
		t.Fatalf("search permission control = %v, want EACCES", err)
	}
	result := stream.readEntry(255)[0].(component.ResultValue)
	if result.IsErr || result.Payload == nil {
		t.Fatalf("read-only directory entry = %#v", result)
	}
	entry := result.Payload.([]component.Value)
	if entry[0] != descriptorRegularFile || entry[1] != "entry" {
		t.Fatalf("read-only entry = %#v", entry)
	}
}
