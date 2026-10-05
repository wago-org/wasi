//go:build darwin

package core

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestAllocateFileAtNonzeroOffset(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "allocate")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	err = allocateFile(f, 4096, 4096)
	if errors.Is(err, unix.EOPNOTSUPP) {
		t.Skip("filesystem does not support preallocation")
	}
	if err != nil {
		t.Fatalf("allocateFile with nonzero offset: %v", err)
	}
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() < 8192 {
		t.Fatalf("size after allocation = %d, want at least 8192", info.Size())
	}
}
