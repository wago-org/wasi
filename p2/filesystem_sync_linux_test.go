//go:build linux

package p2

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSyncDescriptorReadOnlyFIFO(t *testing.T) {
	name := filepath.Join(t.TempDir(), "fifo")
	if err := unix.Mkfifo(name, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Sync(); err == nil {
		t.Fatal("host sync unexpectedly supports this FIFO; fixture cannot prove the no-op")
	}
	if err := syncFileData(f); err == nil {
		t.Fatal("host sync-data unexpectedly supports this FIFO; fixture cannot prove the no-op")
	}
	n := &descriptorNode{file: f, flags: 1}
	for _, dataOnly := range []bool{false, true} {
		if err := syncDescriptor(n, dataOnly); err != nil {
			t.Errorf("read-only FIFO dataOnly=%v: %v, want success", dataOnly, err)
		}
	}
}
