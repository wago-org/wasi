//go:build linux

package p2

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	component "github.com/wago-org/component-model"
	sysunix "golang.org/x/sys/unix"
)

func TestLinuxDirectoryEntryWithZeroInode(t *testing.T) {
	const name = "zero-inode-entry"
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []uint8{sysunix.DT_REG, sysunix.DT_UNKNOWN} {
		label := "known type"
		if typ == sysunix.DT_UNKNOWN {
			label = "unknown type"
		}
		t.Run(label, func(t *testing.T) {
			const nameOffset = int(unsafe.Offsetof(sysunix.Dirent{}.Name))
			length := (nameOffset + len(name) + 1 + 7) &^ 7
			record := make([]byte, length)
			binary.NativeEndian.PutUint16(record[int(unsafe.Offsetof(sysunix.Dirent{}.Reclen)):], uint16(length))
			record[int(unsafe.Offsetof(sysunix.Dirent{}.Type))] = typ
			copy(record[nameOffset:], name)
			gotName, gotType, consumed, err := linuxDirectoryRecord(record)
			if err != nil || string(gotName) != name || gotType != typ || consumed != length {
				t.Fatalf("valid zero-inode record = %q, %d, %d, %v", gotName, gotType, consumed, err)
			}
			file, err := os.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			stream := &directoryStream{file: file}
			stream.reader.buffer = new([8192]byte)
			stream.reader.end = copy(stream.reader.buffer[:], record)
			defer closeDirectoryStream(stream)
			result := stream.readEntry(255)[0].(component.ResultValue)
			if result.IsErr || result.Payload == nil {
				t.Fatalf("zero-inode stream entry = %#v, want a regular file", result)
			}
			entry := result.Payload.([]component.Value)
			if entry[0] != descriptorRegularFile || entry[1] != name {
				t.Fatalf("zero-inode stream entry = %#v, want regular file %q", entry, name)
			}
		})
	}
}
