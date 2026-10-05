//go:build linux

package p2

import (
	"os"
	"path/filepath"
	"testing"

	component "github.com/wago-org/component-model"
)

func TestReadDirectoryRejectsInvalidUTF8Name(t *testing.T) {
	root := t.TempDir()
	name := string([]byte{'x', 0xff})
	if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
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
	result := stream.readEntry(255)[0].(component.ResultValue)
	if !result.IsErr || result.Payload != fsErrIllegalByteSequence {
		t.Fatalf("invalid UTF-8 filename result = %#v, want illegal-byte-sequence", result)
	}
}
