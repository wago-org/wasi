package p2

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	component "github.com/wago-org/component-model"
)

func TestDirectoryEntryStreamSurvivesDirectoryRename(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "original")
	if err := os.MkdirAll(filepath.Join(original, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(original, "file.txt"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := openPreopenDirectory(original, false)
	if err != nil {
		t.Fatal(err)
	}
	streamFile, err := newDirectoryStreamFile(base)
	base.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer streamFile.Close()
	if err := os.Rename(original, filepath.Join(root, "renamed")); err != nil {
		t.Fatal(err)
	}
	stream := &directoryStream{file: streamFile}
	want := map[string]uint32{"child": descriptorDirectory, "file.txt": descriptorRegularFile}
	for range 2 {
		result := stream.readEntry(1 << 20)[0].(component.ResultValue)
		if result.IsErr || result.Payload == nil {
			t.Fatalf("entry after directory rename = %#v, want name and type", result)
		}
		entry := result.Payload.([]component.Value)
		name := entry[1].(string)
		kind, exists := want[name]
		if !exists || entry[0] != kind {
			t.Fatalf("entry after directory rename = %#v, want %v", entry, want)
		}
		delete(want, name)
	}
	result := stream.readEntry(1 << 20)[0].(component.ResultValue)
	if result.IsErr || result.Payload != nil {
		t.Fatalf("end of renamed directory = %#v, want successful EOF", result)
	}
}

func BenchmarkReadDirectoryEntries(b *testing.B) {
	root := b.TempDir()
	const entries = 32
	for i := 0; i < entries; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("entry-%02d", i)), nil, 0o600); err != nil {
			b.Fatal(err)
		}
	}
	base, err := openPreopenDirectory(root, false)
	if err != nil {
		b.Fatal(err)
	}
	defer base.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, err := newDirectoryStreamFile(base)
		if err != nil {
			b.Fatal(err)
		}
		stream := &directoryStream{file: f}
		for j := 0; j < entries; j++ {
			result := stream.readEntry(1 << 20)[0].(component.ResultValue)
			if result.IsErr || result.Payload == nil {
				f.Close()
				b.Fatalf("read entry = %#v", result)
			}
		}
		f.Close()
	}
}
