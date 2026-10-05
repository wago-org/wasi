package p2

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestAsyncInputFinalErrorSurvivesReadiness(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		name := "direct"
		if wrapped {
			name = "consumed-prefix"
		}
		t.Run(name, func(t *testing.T) {
			r := &finalErrorReader{err: io.ErrUnexpectedEOF}
			in := NewInputStream(r)
			state := &hostState{stdin: in}
			if wrapped {
				state.stdin = &prefixedInput{next: in}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := in.WaitReadable(ctx); err != nil {
				t.Fatal(err)
			}
			var payload [3]byte
			if n, err := in.TryRead(payload[:]); n != 3 || err != nil || string(payload[:]) != "abc" {
				t.Fatalf("final payload = %q, %d, %v", payload, n, err)
			}
			ready, err := waitPollables(ctx, []pollableValue{{
				ready: func() bool { return inputReady(state) },
				wait:  in.WaitReadable,
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
			if n, err := state.stdin.TryRead(payload[:]); n != 0 || err != io.ErrUnexpectedEOF {
				t.Fatalf("read after polling = %d, %v; want pending error", n, err)
			}
			if calls := r.calls.Load(); calls != 1 {
				t.Fatalf("reporting pending error made %d underlying reads, want 1", calls)
			}
			if err := state.stdin.WaitReadable(ctx); err != nil {
				t.Fatal(err)
			}
			var next [4]byte
			if n, err := state.stdin.TryRead(next[:]); n != 4 || err != nil || string(next[:]) != "next" {
				t.Fatalf("read after reported error = %q, %d, %v; want next", next, n, err)
			}
			if n, err := state.stdin.TryRead(next[:]); n != 0 || err != io.EOF {
				t.Fatalf("read after next payload = %d, %v; want EOF", n, err)
			}
			if calls := r.calls.Load(); calls != 2 {
				t.Fatalf("resuming made %d underlying reads, want 2", calls)
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
