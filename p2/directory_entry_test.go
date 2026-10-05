package p2

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	component "github.com/wago-org/component-model"
)

func TestDirectoryEntryStreamSurvivesDirectoryRename(t *testing.T) {
	root := directoryTestRoot(t)
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
	// Replacement entries deliberately have different types. A pathname-based
	// metadata lookup must not change the pinned stream's entry types.
	if err := os.MkdirAll(filepath.Join(original, "file.txt"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(original, "child"), nil, 0o600); err != nil {
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

// An optional DT_UNKNOWN filesystem exercises Go 1.22's eager lstat fallback.
// See testdata/directory-unknown for the small functional FUSE fixture.
func directoryTestRoot(t testing.TB) string {
	t.Helper()
	if root := os.Getenv("WASI_TEST_DIRECTORY_ROOT"); root != "" {
		dir, err := os.MkdirTemp(root, "wasi-directory-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.RemoveAll(dir); err != nil {
				t.Error(err)
			}
		})
		return dir
	}
	return t.TempDir()
}

func TestDirectoryEntryStreamAcrossRefills(t *testing.T) {
	root := t.TempDir()
	const count = 257
	want := make(map[string]bool, count)
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("entry-%03d-%080d", i, i)
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		want[name] = true
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
	for range count {
		result := stream.readEntry(255)[0].(component.ResultValue)
		if result.IsErr || result.Payload == nil {
			t.Fatalf("entry = %#v, want %d remaining names", result, len(want))
		}
		entry := result.Payload.([]component.Value)
		name := entry[1].(string)
		if !want[name] || entry[0] != descriptorRegularFile {
			t.Fatalf("entry = %#v, want an unread regular file", entry)
		}
		delete(want, name)
	}
	result := stream.readEntry(255)[0].(component.ResultValue)
	if result.IsErr || result.Payload != nil {
		t.Fatalf("final entry = %#v, want EOF", result)
	}
}

func BenchmarkReadDirectoryEntries(b *testing.B) {
	root := directoryTestRoot(b)
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
				closeDirectoryStream(stream)
				b.Fatalf("read entry = %#v", result)
			}
		}
		closeDirectoryStream(stream)
	}
}
