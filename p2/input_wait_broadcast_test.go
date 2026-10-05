package p2

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Done is evaluated only after WaitReadable has captured the pending read and
// unlocked its mutex. This orders the waiters before releasing the reader.
type enteredReadContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *enteredReadContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

type gatedInputReader struct {
	release <-chan struct{}
	calls   atomic.Int32
}

func (r *gatedInputReader) Read(dst []byte) (int, error) {
	r.calls.Add(1)
	<-r.release
	return copy(dst, "abc"), io.EOF
}

func TestAsyncInputWakesEveryConcurrentWaiter(t *testing.T) {
	for _, cancelOne := range []bool{false, true} {
		name := "all ready"
		if cancelOne {
			name = "one canceled"
		}
		t.Run(name, func(t *testing.T) {
			release := make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			r := &gatedInputReader{release: release}
			in := NewInputStream(r)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			const count = 3
			entered := make([]chan struct{}, count)
			results := make([]chan error, count)
			firstCtx, cancelFirst := context.WithCancel(ctx)
			defer cancelFirst()
			for i := 0; i < count; i++ {
				entered[i] = make(chan struct{})
				results[i] = make(chan error, 1)
				parent := ctx
				if i == 0 {
					parent = firstCtx
				}
				observed := &enteredReadContext{Context: parent, entered: entered[i]}
				result := results[i]
				go func() { result <- in.WaitReadable(observed) }()
			}
			for _, started := range entered {
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("waiter did not enter the readiness wait")
				}
			}
			first := 0
			if cancelOne {
				cancelFirst()
				select {
				case err := <-results[0]:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("canceled waiter = %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("canceled waiter remained blocked")
				}
				first = 1
			}
			releaseOnce.Do(func() { close(release) })
			for i := first; i < count; i++ {
				select {
				case err := <-results[i]:
					if err != nil {
						t.Fatalf("ready waiter %d = %v", i, err)
					}
				case <-time.After(time.Second):
					t.Fatalf("waiter %d remained blocked while input was buffered", i)
				}
			}
			if calls := r.calls.Load(); calls != 1 {
				t.Fatalf("concurrent waiters started %d underlying reads, want 1", calls)
			}
			var dst [3]byte
			if n, err := in.TryRead(dst[:]); n != 3 || err != nil || string(dst[:]) != "abc" {
				t.Fatalf("buffered read = %q, %d, %v", dst, n, err)
			}
			if err := in.WaitReadable(ctx); err != nil {
				t.Fatalf("closed stream readiness = %v", err)
			}
			if n, err := in.TryRead(dst[:]); n != 0 || err != io.EOF {
				t.Fatalf("read after final bytes = %d, %v", n, err)
			}
			if calls := r.calls.Load(); calls != 1 {
				t.Fatalf("closed stream started %d underlying reads, want 1", calls)
			}
		})
	}
}

type broadcastBenchmarkReader struct{}

func (broadcastBenchmarkReader) Read(dst []byte) (int, error) {
	dst[0] = 'x'
	return 1, nil
}

func BenchmarkAsyncInputWaitAndRead(b *testing.B) {
	in := NewInputStream(broadcastBenchmarkReader{})
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
