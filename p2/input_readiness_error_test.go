package p2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
)

type readinessErrorInput struct {
	err      error
	withByte bool
	consumed bool
	calls    int
}

func (in *readinessErrorInput) TryRead(dst []byte) (int, error) {
	in.calls++
	if in.consumed {
		return 0, ErrWouldBlock
	}
	in.consumed = true
	if in.withByte {
		return copy(dst, "x"), in.err
	}
	return 0, in.err
}

func (in *readinessErrorInput) WaitReadable(ctx context.Context) error {
	if !in.consumed {
		return nil
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestInputReadinessPreservesCustomReadError(t *testing.T) {
	for _, operation := range testPollOperations() {
		for _, tc := range []struct {
			name string
			err  error
		}{
			{"host error", io.ErrClosedPipe},
			{"wrapped error", fmt.Errorf("custom input: %w", io.ErrUnexpectedEOF)},
			{"underlying cancellation", context.Canceled},
			{"underlying deadline", context.DeadlineExceeded},
			{"EOF", io.EOF},
			{"wrapped EOF", fmt.Errorf("custom input: %w", io.EOF)},
		} {
			for _, withByte := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/byte=%v", operation.name, tc.name, withByte), func(t *testing.T) {
					in := &readinessErrorInput{err: tc.err, withByte: withByte}
					s := &hostState{stdin: in}
					p := pollableValue{ready: func() bool { return inputReady(s) }, wait: s.waitStdin}
					if err := operation.run(context.Background(), p); err != nil {
						t.Fatalf("polling custom stream failure: %v", err)
					}
					if !inputReady(s) {
						t.Error("repeated readiness lost the probed result")
					}
					var dst [1]byte
					if withByte {
						if n, err := s.readStdin(dst[:]); n != 1 || err != nil || dst[0] != 'x' {
							t.Fatalf("read after polling = %q, %d, %v, want x before error", dst, n, err)
						}
						if !inputReady(s) {
							t.Error("readiness lost the error after delivering its byte")
						}
					}
					if n, err := s.readStdin(dst[:]); n != 0 || err != tc.err {
						t.Errorf("read after polling = %d, %v, want original error %v", n, err, tc.err)
					}
					if in.calls != 1 {
						t.Errorf("reporting the probed result made %d host reads, want 1", in.calls)
					}
					if errors.Is(tc.err, io.EOF) {
						for i := 0; i < 16; i++ {
							if !inputReady(s) {
								t.Fatal("closed custom input stopped being ready")
							}
							if n, err := s.readStdin(dst[:]); n != 0 || !errors.Is(err, io.EOF) {
								t.Fatalf("closed custom read = %d, %v, want EOF", n, err)
							}
						}
						if in.calls != 1 {
							t.Fatalf("closed custom input made %d host reads, want 1", in.calls)
						}
						return
					}
					if n, err := s.readStdin(dst[:]); n != 0 || !errors.Is(err, io.EOF) {
						t.Errorf("read after reported error = %d, %v, want closed", n, err)
					}
					// A reported stream failure permanently closes the guest stream.
					in.consumed, in.withByte, in.err = false, true, nil
					if !inputReady(s) {
						t.Fatal("closed input stopped being ready")
					}
					if n, err := s.readStdin(dst[:]); n != 0 || !errors.Is(err, io.EOF) {
						t.Errorf("closed input = %d, %v, want EOF", n, err)
					}
					if in.calls != 1 {
						t.Errorf("closed input made %d host reads, want 1", in.calls)
					}
				})
			}
		}
	}
}

func TestInputReadinessPreservesWrappedWouldBlock(t *testing.T) {
	in := &readinessErrorInput{err: fmt.Errorf("not ready: %w", ErrWouldBlock)}
	s := &hostState{stdin: in}
	if inputReady(s) {
		t.Fatal("wrapped would-block was reported as readiness")
	}
}

func TestInputReadinessCachedResultDoesNotWait(t *testing.T) {
	for _, withByte := range []bool{false, true} {
		in := &readinessErrorInput{err: io.ErrClosedPipe, withByte: withByte}
		s := &hostState{stdin: in}
		if !inputReady(s) {
			t.Fatal("custom result was not ready")
		}
		// The underlying stream has consumed its one-shot result and would
		// wait for cancellation. Buffered results must bypass that wait, just
		// as a buffered asyncInput does, even with an already canceled context.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := s.waitStdin(ctx); err != nil {
			t.Fatalf("wait for cached result byte=%v: %v", withByte, err)
		}
	}
}

func TestInputReadinessPreservesCallerCancellation(t *testing.T) {
	for _, operation := range testPollOperations() {
		t.Run(operation.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			in := &readinessErrorInput{err: io.ErrClosedPipe, consumed: true}
			s := &hostState{stdin: in}
			err := operation.run(ctx, pollableValue{
				ready: func() bool { return inputReady(s) },
				wait: func(context.Context) error {
					in.consumed = false
					cancel()
					return ctx.Err()
				},
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled poll = %v, want caller cancellation", err)
			}
		})
	}
}

func BenchmarkInputReadinessProbe(b *testing.B) {
	for _, tc := range []struct {
		name string
		err  error
		data bool
	}{
		{"data", nil, true},
		{"error", io.ErrClosedPipe, false},
		{"would-block", ErrWouldBlock, false},
	} {
		b.Run(tc.name, func(b *testing.B) {
			in := &readinessErrorInput{err: tc.err, withByte: tc.data}
			s := &hostState{stdin: in}
			var dst [1]byte
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				in.consumed = false
				if inputReady(s) {
					_, _ = s.readStdin(dst[:])
				}
			}
		})
	}
}
