package p2

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

type finalErrorReader struct {
	err   error
	calls atomic.Int32
}

func (r *finalErrorReader) Read(p []byte) (int, error) {
	if r.calls.Add(1) == 1 {
		return copy(p, "abc"), r.err
	}
	return copy(p, "next"), io.EOF
}

func TestAsyncInputPreservesFinalReadError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"non-EOF", io.ErrUnexpectedEOF},
		{"wrapped non-EOF", fmt.Errorf("reader failed: %w", io.ErrClosedPipe)},
		{"EOF", io.EOF},
	} {
		for _, size := range []int{1, 3} {
			t.Run(fmt.Sprintf("%s/chunk-%d", tc.name, size), func(t *testing.T) {
				r := &finalErrorReader{err: tc.err}
				in := NewInputStream(r)
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := in.WaitReadable(ctx); err != nil {
					t.Fatal(err)
				}
				buf := make([]byte, size)
				var got string
				for len(got) < 3 {
					n, err := in.TryRead(buf)
					if err != nil || n == 0 {
						t.Fatalf("payload read = %d, %v; want bytes without error", n, err)
					}
					got += string(buf[:n])
				}
				if got != "abc" {
					t.Fatalf("payload = %q, want abc", got)
				}
				if err := in.WaitReadable(ctx); err != nil {
					t.Fatalf("wait for pending error: %v", err)
				}
				if n, err := in.TryRead(buf); n != 0 || err != tc.err {
					t.Fatalf("read after final bytes = %d, %v; want 0, %v", n, err, tc.err)
				}
				if got := r.calls.Load(); got != 1 {
					t.Fatalf("reporting pending error made %d underlying reads, want 1", got)
				}
				if tc.err == io.EOF {
					if n, err := in.TryRead(buf); n != 0 || err != io.EOF {
						t.Fatalf("closed read = %d, %v", n, err)
					}
					if got := r.calls.Load(); got != 1 {
						t.Fatalf("closed read made %d underlying reads, want 1", got)
					}
					return
				}
				// A non-EOF failure is delivered once; a later read can resume.
				if err := in.WaitReadable(ctx); err != nil {
					t.Fatal(err)
				}
				var next [4]byte
				if n, err := in.TryRead(next[:]); n != 4 || err != nil || string(next[:]) != "next" {
					t.Fatalf("read after reported error = %q, %d, %v; want next, 4, nil", next, n, err)
				}
				if n, err := in.TryRead(next[:]); n != 0 || err != io.EOF {
					t.Fatalf("read after second payload = %d, %v; want EOF", n, err)
				}
				if got := r.calls.Load(); got != 2 {
					t.Fatalf("resuming made %d underlying reads, want 2", got)
				}
			})
		}
	}
}

func BenchmarkAsyncInputFinalBytes(b *testing.B) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"success", nil},
		{"non-EOF", io.ErrUnexpectedEOF},
		{"EOF", io.EOF},
	} {
		b.Run(tc.name, func(b *testing.B) {
			var payload [3]byte
			var dst [3]byte
			result := readResult{err: tc.err}
			in := &asyncInput{}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result.b = payload[:]
				in.result = &result
				if n, err := in.TryRead(dst[:]); n != 3 || err != nil {
					b.Fatalf("read = %d, %v", n, err)
				}
			}
		})
	}
}

type repeatedInputReader struct{}

func (repeatedInputReader) Read(p []byte) (int, error) {
	p[0] = 'x'
	return 1, nil
}

func BenchmarkAsyncInputRead(b *testing.B) {
	in := NewInputStream(repeatedInputReader{})
	var dst [1]byte
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := in.WaitReadable(context.Background()); err != nil {
			b.Fatal(err)
		}
		if n, err := in.TryRead(dst[:]); n != 1 || err != nil {
			b.Fatalf("read = %d, %v", n, err)
		}
	}
}
