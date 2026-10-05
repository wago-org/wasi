package core

import (
	"context"
	"encoding/binary"
	"sync/atomic"
	"testing"
)

type spuriousPollInput struct {
	waits atomic.Int32
}

func (*spuriousPollInput) Read([]byte) (int, error) { return 0, nil }
func (p *spuriousPollInput) Ready() bool            { return p.waits.Load() >= 2 }
func (p *spuriousPollInput) Wait(context.Context) error {
	p.waits.Add(1)
	return nil
}

func TestPollOneoffRetriesSpuriousStreamWake(t *testing.T) {
	input := new(spuriousPollInput)
	e := newTestPlugin(t, Config{Stdin: input})
	mem := make([]byte, 128)
	putFDSubscription(mem, 0, 42, 1, 0)
	r := make([]uint64, 1)
	e.pollOneoff(testModule{mem: mem}, []uint64{0, 48, 1, 80}, r)
	if r[0] != wasiOK {
		t.Fatalf("poll_oneoff errno = %d, want success", r[0])
	}
	if got := binary.LittleEndian.Uint32(mem[80:]); got != 1 {
		t.Fatalf("poll_oneoff events = %d after %d waits, want one ready event", got, input.waits.Load())
	}
	if got := binary.LittleEndian.Uint64(mem[48:]); got != 42 {
		t.Fatalf("poll_oneoff userdata = %d, want 42", got)
	}
}
