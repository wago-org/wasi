package p2

import (
	"errors"
	"os"
	"testing"
)

func TestSyncDescriptorWithoutWriteDoesNotCallHost(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "closed-host-handle")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	// A closed host file makes any host sync fail on every platform. The
	// descriptor's flags alone must make both WASI operations successful.
	for _, flags := range []uint32{0, 1, 1 << 5} {
		for _, dataOnly := range []bool{false, true} {
			n := &descriptorNode{file: f, flags: flags}
			if err := syncDescriptor(n, dataOnly); err != nil {
				t.Errorf("flags=%#x dataOnly=%v: %v, want success without host sync", flags, dataOnly, err)
			}
		}
	}
}

func TestSyncDescriptorWithWritePreservesHostResult(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "writable")
	if err != nil {
		t.Fatal(err)
	}
	n := &descriptorNode{file: f, flags: 2}
	for _, dataOnly := range []bool{false, true} {
		if err := syncDescriptor(n, dataOnly); err != nil {
			t.Errorf("writable dataOnly=%v: %v", dataOnly, err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	for _, dataOnly := range []bool{false, true} {
		if err := syncDescriptor(n, dataOnly); err == nil {
			t.Errorf("closed writable dataOnly=%v: host error was lost", dataOnly)
		}
	}
	if err := syncDescriptor(n, false); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed writable sync = %v, want ErrClosed", err)
	}
}

func BenchmarkSyncDescriptorReadOnly(b *testing.B) {
	f, err := os.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	n := &descriptorNode{file: f, flags: 1, isDir: true}
	for _, dataOnly := range []bool{false, true} {
		name := "sync"
		if dataOnly {
			name = "sync-data"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := syncDescriptor(n, dataOnly); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
