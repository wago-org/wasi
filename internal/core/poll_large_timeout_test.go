package core

import (
	"context"
	"encoding/binary"
	"testing"
	"time"
)

func TestPollReadyFDWithLargeClockTimeout(t *testing.T) {
	e := newTestPlugin(t, Config{Clocks: fixedClocks{}})
	mem := make([]byte, 512)
	putClockSubscription(mem, 0, 1, 1, uint64(1)<<63, false)
	putFDSubscription(mem, 48, 2, 2, 1)
	code, count := poll(t, e, mem, 2)
	if code != wasiOK || count != 1 {
		t.Fatalf("poll = (%d, %d events), want (OK, 1)", code, count)
	}
	if got := binary.LittleEndian.Uint64(mem[256:]); got != 2 {
		t.Fatalf("event userdata = %d, want ready FD userdata 2", got)
	}
}

func TestPollLargeClockTimeoutAfterSpuriousWake(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	input := new(spuriousPollInput)
	e := newTestPlugin(t, Config{Clocks: fixedClocks{}, Context: ctx, Stdin: input})
	mem := make([]byte, 512)
	putClockSubscription(mem, 0, 1, 1, uint64(1)<<63, false)
	putFDSubscription(mem, 48, 2, 1, 0)
	code, count := poll(t, e, mem, 2)
	if code != wasiOK || count != 1 {
		t.Fatalf("poll after spurious wake = (%d, %d events), want (OK, 1)", code, count)
	}
	if got := binary.LittleEndian.Uint64(mem[256:]); got != 2 {
		t.Fatalf("event userdata = %d, want ready FD userdata 2", got)
	}
	if got := input.waits.Load(); got != 2 {
		t.Fatalf("wait calls = %d, want two including spurious wake", got)
	}
}
