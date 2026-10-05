//go:build linux

package p2

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestStatUnderDoesNotWaitForFIFOWriter(t *testing.T) {
	root := t.TempDir()
	if err := unix.Mkfifo(root+"/pipe", 0o600); err != nil {
		t.Fatal(err)
	}
	dir, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	type result struct {
		mode os.FileMode
		err  error
	}
	done := make(chan result, 1)
	go func() {
		info, err := statUnder(dir, "pipe")
		if err != nil {
			done <- result{err: err}
			return
		}
		done <- result{mode: info.Mode()}
	}()
	select {
	case got := <-done:
		if got.err != nil || got.mode&os.ModeNamedPipe == 0 {
			t.Fatalf("FIFO stat = %v, %v", got.mode, got.err)
		}
	case <-time.After(time.Second):
		writer, err := os.OpenFile(root+"/pipe", os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		writer.Close()
		<-done
		t.Fatal("FIFO metadata lookup waited for a writer")
	}
}
