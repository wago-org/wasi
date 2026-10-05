package p2

import (
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestConfiguredMountPreservesExactDescriptorFlags(t *testing.T) {
	fs := newFilesystem([]Preopen{{GuestPath: "/data", HostPath: t.TempDir(), Read: true}}, Limits{})
	if got, want := len(fs.mounts), 1; got != want {
		t.Fatalf("mount count = %d, want %d", got, want)
	}
	if got, want := fs.mounts[0].flags, uint32(1); got != want {
		t.Fatalf("mount flags = %#x, want %#x", got, want)
	}

	base := &descriptorNode{flags: fs.mounts[0].flags, isDir: true}
	if err := validateChildFlags(base, 1, 0); err != nil {
		t.Fatalf("read child rejected: %v", err)
	}
	if err := validateChildFlags(base, 0, 0); err != nil {
		t.Fatalf("zero-flag child rejected: %v", err)
	}
	for name, requested := range map[string]uint32{"write": 2, "mutate-directory": 1 << 5} {
		if err := validateChildFlags(base, requested, 0); err == nil {
			t.Fatalf("%s child escalated from read-only base", name)
		}
	}
	if err := validateChildFlags(base, 1, 1); err == nil {
		t.Fatal("create through read-only base succeeded")
	}
	writeWithoutMutation := &descriptorNode{flags: 1 | 2, isDir: true}
	if err := validateChildFlags(writeWithoutMutation, 2, 0); err == nil {
		t.Fatal("writable child recovered mutate-directory authority")
	}
}

func TestPrepareFilesystemPinsValidatedPreopen(t *testing.T) {
	parent := t.TempDir()
	original := parent + "/mounted"
	if err := os.Mkdir(original, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original+"/inside", []byte("pinned"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs, err := prepareFilesystem([]Preopen{{GuestPath: "/data", HostPath: original, Read: true}}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer fs.closeMounts()
	if err := os.Rename(original, parent+"/moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(original, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := openUnder(fs.mounts[0].base, "inside", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("open through pinned preopen: %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil || string(data) != "pinned" {
		t.Fatalf("pinned content = %q, %v", data, err)
	}
}

func TestCheckedOffsetRejectsSignedOverflow(t *testing.T) {
	if got, err := checkedOffset(math.MaxInt64); err != nil || got != math.MaxInt64 {
		t.Fatalf("MaxInt64 = %d, %v", got, err)
	}
	if _, err := checkedOffset(uint64(math.MaxInt64) + 1); err == nil {
		t.Fatal("MaxInt64+1 offset succeeded")
	}
}

func TestAppendStreamsDoNotOverlap(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "append")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	target := &appendTarget{}
	a := &fileStream{file: f, append: target}
	b := &fileStream{file: f, append: target}

	const records = 100
	var wg sync.WaitGroup
	for streamIndex, stream := range []*fileStream{a, b} {
		streamIndex, stream := streamIndex, stream
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < records; i++ {
				record := fmt.Sprintf("%d:%03d\n", streamIndex, i)
				if n, err := stream.Write([]byte(record)); err != nil || n != len(record) {
					t.Errorf("append %q = %d, %v", record, n, err)
					return
				}
			}
		}()
	}
	wg.Wait()

	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if got, want := len(lines), records*2; got != want {
		t.Fatalf("appended record count = %d, want %d", got, want)
	}
	seen := make(map[string]bool, len(lines))
	for _, line := range lines {
		if seen[line] {
			t.Fatalf("duplicate/overlapping append record %q", line)
		}
		seen[line] = true
	}
}

func TestAppendIndependentDescriptorsDoNotOverlap(t *testing.T) {
	const writers, records = 8, 250
	path := t.TempDir() + "/append"
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fs := newFilesystem(nil, Limits{})
	streams := make([]*fileStream, writers)
	for i := range streams {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		node := &descriptorNode{file: f}
		if _, err := fs.addDesc(node); err != nil {
			t.Fatal(err)
		}
		streams[i] = &fileStream{file: f, append: node.append}
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, stream := range streams {
		i, stream := i, stream
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < records; j++ {
				record := fmt.Sprintf("%02d:%04d\n", i, j)
				if n, err := stream.Write([]byte(record)); err != nil || n != len(record) {
					t.Errorf("append %q = %d, %v", record, n, err)
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(data), writers*records*8; got != want {
		t.Fatalf("appended bytes = %d, want %d; concurrent append lost records", got, want)
	}
}

func TestAppendStreamSurvivesParentDescriptorClose(t *testing.T) {
	parent, err := os.CreateTemp(t.TempDir(), "append-parent")
	if err != nil {
		t.Fatal(err)
	}
	streamFile, err := dupFile(parent)
	if err != nil {
		parent.Close()
		t.Fatal(err)
	}
	target := &appendTarget{}
	stream := &fileStream{file: streamFile, append: target}
	if err := parent.Close(); err != nil {
		streamFile.Close()
		t.Fatal(err)
	}
	defer streamFile.Close()
	if n, err := stream.Write([]byte("still-open")); err != nil || n != len("still-open") {
		t.Fatalf("append after descriptor close = %d, %v", n, err)
	}
	data, err := os.ReadFile(parent.Name())
	if err != nil || string(data) != "still-open" {
		t.Fatalf("content after descriptor close = %q, %v", data, err)
	}
}
