package p2

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestAsyncInputFinalErrorSurvivesReadiness(t *testing.T) {
	for _, probed := range []bool{false, true} {
		name := "direct"
		if probed {
			name = "after-readiness-probe"
		}
		t.Run(name, func(t *testing.T) {
			r := &finalErrorReader{err: io.ErrUnexpectedEOF}
			in := NewInputStream(r)
			state := &hostState{stdin: in}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := in.WaitReadable(ctx); err != nil {
				t.Fatal(err)
			}
			if probed && !inputReady(state) {
				t.Fatal("payload was not ready")
			}
			var payload [3]byte
			for got := 0; got < len(payload); {
				n, err := state.readStdin(payload[got:])
				if n == 0 || err != nil {
					t.Fatalf("final payload read = %d, %v", n, err)
				}
				got += n
			}
			if string(payload[:]) != "abc" {
				t.Fatalf("final payload = %q, want abc", payload)
			}
			ready, err := waitPollables(ctx, []pollableValue{{
				ready: func() bool { return inputReady(state) },
				wait:  state.waitStdin,
			}})
			if err != nil || len(ready) != 1 || ready[0] != uint32(0) {
				t.Fatalf("poll for pending error = %v, %v; want index 0", ready, err)
			}
			for i := 0; i < 32; i++ {
				if !inputReady(state) {
					t.Fatalf("readiness probe %d consumed the pending error", i)
				}
				if calls := r.calls.Load(); calls != 1 {
					t.Fatalf("readiness probe %d made %d underlying reads, want 1", i, calls)
				}
			}
			if n, err := state.readStdin(payload[:]); n != 0 || err != io.ErrUnexpectedEOF {
				t.Fatalf("read after polling = %d, %v; want pending error", n, err)
			}
			if calls := r.calls.Load(); calls != 1 {
				t.Fatalf("reporting pending error made %d underlying reads, want 1", calls)
			}
			if err := state.waitStdin(ctx); err != nil {
				t.Fatal(err)
			}
			var next [4]byte
			if n, err := state.readStdin(next[:]); n != 0 || err != io.EOF {
				t.Fatalf("read after reported error = %d, %v; want EOF", n, err)
			}
			if calls := r.calls.Load(); calls != 1 {
				t.Fatalf("closed stream made %d underlying reads, want 1", calls)
			}
		})
	}
}

func BenchmarkInputReadyPendingError(b *testing.B) {
	in := &asyncInput{result: &readResult{err: io.ErrUnexpectedEOF}}
	state := &hostState{stdin: in}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !inputReady(state) {
			b.Fatal("pending error is not ready")
		}
	}
}
