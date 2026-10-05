package p2

import (
	"context"
	"io"
	"testing"
)

type failedWaitInput struct{ waits, reads int }

func (in *failedWaitInput) WaitReadable(context.Context) error {
	in.waits++
	return io.ErrClosedPipe
}
func (in *failedWaitInput) TryRead([]byte) (int, error) {
	in.reads++
	return 0, ErrWouldBlock
}

func TestInputPollReportsWaitFailureAsReadinessThenCloses(t *testing.T) {
	in := &failedWaitInput{}
	s := &hostState{stdin: in}
	ready, err := waitPollables(context.Background(), []pollableValue{{
		ready: func() bool { return inputReady(s) },
		wait:  s.waitStdin,
	}})
	if err != nil || len(ready) != 1 || ready[0] != uint32(0) {
		t.Fatalf("poll after input failure = %v, %v; want ready index 0", ready, err)
	}
	var dst [1]byte
	if n, err := s.readStdin(dst[:]); n != 0 || err != io.ErrClosedPipe {
		t.Fatalf("first read = %d, %v; want original error", n, err)
	}
	if n, err := s.readStdin(dst[:]); n != 0 || err != io.EOF {
		t.Fatalf("second read = %d, %v; want closed", n, err)
	}
	if in.waits != 1 || in.reads != 1 {
		t.Fatalf("underlying input waits=%d reads=%d, want one of each", in.waits, in.reads)
	}
}
