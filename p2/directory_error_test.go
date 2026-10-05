package p2

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	component "github.com/wago-org/component-model"
)

func TestDirectoryReadEntryReturnsHostError(t *testing.T) {
	file, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	entries, readErr := file.ReadDir(1)
	if len(entries) != 0 || readErr == nil || errors.Is(readErr, io.EOF) {
		t.Fatalf("closed host directory ReadDir=%v, %v; need zero entries with a non-EOF error", entries, readErr)
	}
	stream := directoryStream{file: file}
	values := stream.readEntry(255)
	if len(values) != 1 {
		t.Fatalf("result count=%d, want 1", len(values))
	}
	result, ok := values[0].(component.ResultValue)
	if !ok || !result.IsErr || result.Payload != fsError(readErr) {
		t.Fatalf("directory entry result=%#v; want error code %d for host error %v", values[0], fsError(readErr), readErr)
	}
}

func TestDirectoryReadEntryPreservesEndOfStream(t *testing.T) {
	file, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	entries, readErr := file.ReadDir(1)
	if len(entries) != 0 || !errors.Is(readErr, io.EOF) {
		t.Fatalf("empty host directory ReadDir=%v, %v; want EOF", entries, readErr)
	}
	stream := directoryStream{file: file}
	for index := 0; index < 2; index++ {
		values := stream.readEntry(255)
		if len(values) != 1 {
			t.Fatalf("result count=%d, want 1", len(values))
		}
		result, ok := values[0].(component.ResultValue)
		if !ok || result.IsErr || result.Payload != nil {
			t.Fatalf("EOF result=%#v; want ok(none)", values[0])
		}
	}
}

func TestDirectoryReadEntryAfterClose(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	file, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	stream := &directoryStream{file: file}
	first := stream.readEntry(255)[0].(component.ResultValue)
	if first.IsErr || first.Payload == nil {
		t.Fatalf("first entry = %#v, want an entry", first)
	}
	if err := closeDirectoryStream(stream); err != nil {
		t.Fatal(err)
	}
	_, readErr := file.ReadDir(1)
	if readErr == nil || errors.Is(readErr, io.EOF) {
		t.Fatalf("closed host directory read = %v, want a non-EOF error", readErr)
	}
	result := stream.readEntry(255)[0].(component.ResultValue)
	if !result.IsErr || result.Payload != fsError(readErr) {
		t.Fatalf("entry after close = %#v, want host error %d", result, fsError(readErr))
	}
}

func TestDirectoryEntryNameLimit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "long-name"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	stream := &directoryStream{file: file}
	defer closeDirectoryStream(stream)
	result := stream.readEntry(3)[0].(component.ResultValue)
	if !result.IsErr || result.Payload != fsError(hostFS.ENAMETOOLONG) {
		t.Fatalf("long entry = %#v, want name-too-long", result)
	}
	result = stream.readEntry(255)[0].(component.ResultValue)
	if result.IsErr || result.Payload != nil {
		t.Fatalf("entry after consumed long name = %#v, want EOF", result)
	}
}

func BenchmarkDirectoryReadEntryEOF(b *testing.B) {
	file, err := os.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer file.Close()
	stream := directoryStream{file: file}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		values := stream.readEntry(255)
		if len(values) != 1 || values[0].(component.ResultValue).IsErr {
			b.Fatal("unexpected EOF result")
		}
	}
}
