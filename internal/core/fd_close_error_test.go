package core

import (
	"os"
	"testing"
)

func TestFDCloseReleasesDescriptorAfterHostCloseError(t *testing.T) {
	e := newTestPlugin(t, Config{})
	f, err := os.CreateTemp(t.TempDir(), "already-closed")
	if err != nil {
		t.Fatal(err)
	}
	e.fs.fds[3] = &fdEntry{file: f}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	r := make([]uint64, 1)
	e.fdClose(testModule{}, []uint64{3}, r)
	if r[0] == wasiOK {
		t.Fatal("fd_close unexpectedly succeeded after host close error")
	}
	if _, code := e.entry(3); code != wasiEBadf {
		t.Fatalf("entry after fd_close error = %d, want EBADF", code)
	}
}
