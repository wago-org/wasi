package core

import (
	"context"
	"encoding/binary"
	"os"
	"testing"
	"time"
)

func TestPollClosedHostFileReportsDescriptorError(t *testing.T) {
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer wr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	e := newTestPlugin(t, Config{Context: ctx, Stdin: rd})
	if err := rd.Close(); err != nil {
		t.Fatal(err)
	}

	mem := make([]byte, 256)
	putFDSubscription(mem, 0, 7, 1, 0)
	r := make([]uint64, 1)
	e.pollOneoff(testModule{mem}, []uint64{0, 128, 1, 120}, r)
	if r[0] != wasiOK || binary.LittleEndian.Uint32(mem[120:]) != 1 {
		t.Fatalf("poll = (%d, %d events), want one descriptor-error event", r[0], binary.LittleEndian.Uint32(mem[120:]))
	}
	if got := binary.LittleEndian.Uint16(mem[128+8:]); got != wasiEBadf {
		t.Fatalf("closed file event errno = %d, want EBADF", got)
	}
}
