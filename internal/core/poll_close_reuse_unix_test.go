//go:build darwin || linux

package core

import (
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"regexp"
	"testing"
	"time"
)

func TestPollClosedHostFDIsNotConfusedWithReusedFD(t *testing.T) {
	const child = "WASI_POLL_REUSED_FD_CHILD"
	if os.Getenv(child) != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+regexp.QuoteMeta(t.Name())+"$")
		cmd.Env = append(os.Environ(), child+"=1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("reused-fd poll child: %v\n%s", err, output)
		}
		return
	}

	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer wr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	e := newTestPlugin(t, Config{Context: ctx, Stdin: rd})
	mem := make([]byte, 256)
	putFDSubscription(mem, 0, 7, 1, 0)
	r := make([]uint64, 1)
	done := make(chan struct{})
	go func() {
		e.pollOneoff(testModule{mem}, []uint64{0, 128, 1, 120}, r)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("unreadable pipe reported ready before close")
	case <-time.After(20 * time.Millisecond):
	}
	oldFD := rd.Fd()
	if err := rd.Close(); err != nil {
		t.Fatal(err)
	}
	replacementRead, replacementWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer replacementRead.Close()
	defer replacementWrite.Close()
	if replacementRead.Fd() != oldFD {
		t.Skipf("host did not reuse closed fd %d (new read fd %d)", oldFD, replacementRead.Fd())
	}
	select {
	case <-done:
	case <-time.After(150 * time.Millisecond):
		t.Fatal("poll waited on the unrelated replacement pipe")
	}
	if r[0] != wasiOK || binary.LittleEndian.Uint32(mem[120:]) != 1 {
		t.Fatalf("poll = (%d, %d events), want one descriptor-error event", r[0], binary.LittleEndian.Uint32(mem[120:]))
	}
	if got := binary.LittleEndian.Uint16(mem[128+8:]); got != wasiEBadf {
		t.Fatalf("closed file event errno = %d, want EBADF", got)
	}
}
