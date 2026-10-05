package p2

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"
)

func testPollOperations() []struct {
	name string
	run  func(context.Context, pollableValue) error
} {
	return []struct {
		name string
		run  func(context.Context, pollableValue) error
	}{
		{"block", blockPollable},
		{"poll", func(ctx context.Context, p pollableValue) error {
			ready, err := waitPollables(ctx, []pollableValue{p})
			if err == nil && (len(ready) != 1 || ready[0] != uint32(0)) {
				return fmt.Errorf("poll returned readiness %v, want index 0", ready)
			}
			return err
		}},
	}
}

func TestPollOperationsPreserveCancellationWhenWaitMakesReady(t *testing.T) {
	for _, operation := range testPollOperations() {
		for _, tc := range []struct {
			name       string
			deadline   bool
			wrapped    bool
			successful bool
		}{
			{"canceled", false, false, false},
			{"deadline", true, false, false},
			{"wrapped-canceled", false, true, false},
			{"wrapped-deadline", true, true, false},
			{"caller-canceled-after-success", false, false, true},
			{"caller-deadline-after-success", true, false, true},
		} {
			t.Run(operation.name+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				if tc.deadline {
					cancel()
					ctx, cancel = context.WithDeadline(context.Background(), time.Unix(1, 0))
				}
				defer cancel()
				ready := false
				err := operation.run(ctx, pollableValue{
					ready: func() bool { return ready },
					wait: func(context.Context) error {
						ready = true
						cancel()
						if tc.successful {
							return nil
						}
						if tc.wrapped {
							return fmt.Errorf("wait: %w", ctx.Err())
						}
						return ctx.Err()
					},
				})
				if want := ctx.Err(); want == nil || err != want {
					t.Fatalf("wait made ready: %v, want caller error %v", err, want)
				}
			})
		}
	}
}

type contextFailureWriter struct {
	err              error
	started, release chan struct{}
}

func (w *contextFailureWriter) Write([]byte) (int, error) {
	close(w.started)
	<-w.release
	return 0, w.err
}

func TestPollOperationsReportContextWriterFailuresAsReadiness(t *testing.T) {
	for _, operation := range testPollOperations() {
		for _, tc := range []struct {
			name string
			err  error
		}{
			{"canceled", context.Canceled},
			{"deadline", context.DeadlineExceeded},
			{"wrapped-canceled", fmt.Errorf("write: %w", context.Canceled)},
			{"wrapped-deadline", fmt.Errorf("write: %w", context.DeadlineExceeded)},
		} {
			t.Run(operation.name+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				w := &contextFailureWriter{err: tc.err, started: make(chan struct{}), release: make(chan struct{})}
				defer func() {
					select {
					case <-w.release:
					default:
						close(w.release)
					}
				}()
				out := NewOutputStream(w)
				if err := out.TryWrite([]byte("payload")); err != nil {
					t.Fatal(err)
				}
				select {
				case <-w.started:
				case <-ctx.Done():
					t.Fatal("writer did not start")
				}
				err := operation.run(ctx, pollableValue{
					ready: func() bool { n, err := out.CheckWrite(); return n > 0 || err != nil },
					wait: func(ctx context.Context) error {
						close(w.release)
						return out.WaitWritable(ctx)
					},
				})
				if err != nil || ctx.Err() != nil {
					t.Fatalf("polling writer failure = %v, caller error = %v; want readiness", err, ctx.Err())
				}
				if _, err := out.CheckWrite(); err != tc.err {
					t.Fatalf("writer error = %v, want %v", err, tc.err)
				}
			})
		}
	}
}

func TestPollCancelsLosingWaitersWithoutCancelingCaller(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	loserStarted := make(chan struct{})
	loserStopped := make(chan struct{})
	winnerReady := false
	ready, err := waitPollables(ctx, []pollableValue{
		{
			ready: func() bool { return winnerReady },
			wait: func(context.Context) error {
				<-loserStarted
				winnerReady = true
				return nil
			},
		},
		{
			ready: func() bool { return false },
			wait: func(ctx context.Context) error {
				close(loserStarted)
				<-ctx.Done()
				close(loserStopped)
				return ctx.Err()
			},
		},
	})
	if err != nil || len(ready) != 1 || ready[0] != uint32(0) {
		t.Fatalf("poll = %v, %v; want ready index 0", ready, err)
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("caller context canceled: %v", err)
	}
	select {
	case <-loserStopped:
	case <-ctx.Done():
		t.Fatal("losing waiter was not canceled")
	}
}

func BenchmarkBlockPollableOutputErrorReadiness(b *testing.B) {
	ready := false
	p := pollableValue{
		ready: func() bool { return ready },
		wait: func(context.Context) error {
			ready = true
			return io.ErrClosedPipe
		},
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ready = false
		if err := blockPollable(ctx, p); err != nil {
			b.Fatal(err)
		}
	}
}
