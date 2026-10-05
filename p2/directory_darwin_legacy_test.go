//go:build darwin && !go1.27

package p2

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	component "github.com/wago-org/component-model"
	sysunix "golang.org/x/sys/unix"
)

func darwinTestDirectoryRecord(name string, typ uint8) []byte {
	const offset = int(unsafe.Offsetof(sysunix.Dirent{}.Name))
	record := make([]byte, (offset+len(name)+1+7)&^7)
	binary.NativeEndian.PutUint64(record, 1)
	binary.NativeEndian.PutUint16(record[int(unsafe.Offsetof(sysunix.Dirent{}.Reclen)):], uint16(len(record)))
	binary.NativeEndian.PutUint16(record[int(unsafe.Offsetof(sysunix.Dirent{}.Namlen)):], uint16(len(name)))
	record[int(unsafe.Offsetof(sysunix.Dirent{}.Type))] = typ
	copy(record[offset:], name)
	return record
}

func TestDarwinDirectoryRecordBounds(t *testing.T) {
	valid := darwinTestDirectoryRecord("entry", sysunix.DT_REG)
	name, typ, consumed, err := darwinDirectoryRecord(valid)
	if err != nil || string(name) != "entry" || typ != sysunix.DT_REG || consumed != len(valid) {
		t.Fatalf("valid record = %q, %d, %d, %v", name, typ, consumed, err)
	}
	for n := 0; n < len(valid); n++ {
		if _, _, _, err := darwinDirectoryRecord(valid[:n]); !errors.Is(err, sysunix.EIO) {
			t.Fatalf("truncated length %d: %v", n, err)
		}
	}
	for _, test := range []struct {
		name   string
		mutate func([]byte)
	}{
		{"zero length", func(b []byte) { binary.NativeEndian.PutUint16(b[int(unsafe.Offsetof(sysunix.Dirent{}.Reclen)):], 0) }},
		{"name beyond record", func(b []byte) {
			binary.NativeEndian.PutUint16(b[int(unsafe.Offsetof(sysunix.Dirent{}.Namlen)):], uint16(len(b)))
		}},
		{"missing terminator", func(b []byte) { b[int(unsafe.Offsetof(sysunix.Dirent{}.Name))+5] = 'x' }},
		{"embedded terminator", func(b []byte) { b[int(unsafe.Offsetof(sysunix.Dirent{}.Name))+1] = 0 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := append([]byte(nil), valid...)
			test.mutate(b)
			if _, _, _, err := darwinDirectoryRecord(b); !errors.Is(err, sysunix.EIO) {
				t.Fatalf("malformed record: %v", err)
			}
		})
	}
	removed := append([]byte(nil), valid...)
	binary.NativeEndian.PutUint64(removed, 0)
	if name, _, n, err := darwinDirectoryRecord(removed); err != nil || len(name) != 0 || n != len(removed) {
		t.Fatalf("removed record = %q,%d,%v", name, n, err)
	}
}

// Exercise the descriptor-relative unknown-type fallback independently of the
// native filesystem's choice to return cached types.
func TestDarwinUnknownDirectoryEntryUsesPinnedDescriptor(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "original")
	if err := os.Mkdir(original, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(original, "entry"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(original)
	if err != nil {
		t.Fatal(err)
	}
	stream := &directoryStream{file: file}
	defer closeDirectoryStream(stream)
	if err := os.Rename(original, filepath.Join(root, "renamed")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(original, "entry"), 0700); err != nil {
		t.Fatal(err)
	}
	stream.reader.buffer = new([8192]byte)
	stream.reader.end = copy(stream.reader.buffer[:], darwinTestDirectoryRecord("entry", sysunix.DT_UNKNOWN))
	result := stream.readEntry(255)[0].(component.ResultValue)
	if result.IsErr || result.Payload == nil {
		t.Fatalf("pinned entry = %#v", result)
	}
	entry := result.Payload.([]component.Value)
	if entry[0] != descriptorRegularFile || entry[1] != "entry" {
		t.Fatalf("pinned entry = %#v, want original regular file", entry)
	}
}
