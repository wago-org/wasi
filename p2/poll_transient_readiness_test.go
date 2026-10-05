package p2

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestBlockRetriesWhenReadinessIsConsumedBeforeRecheck(t *testing.T) {
	waits := 0
	ready := false
	p := pollableValue{
		ready: func() bool { return ready },
		wait: func(context.Context) error {
			waits++
			if waits == 2 {
				ready = true
			}
			return nil
		},
	}
	if err := blockPollable(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if waits != 2 || !ready {
		t.Fatalf("block returned after %d waits with readiness %t; want two waits and readiness", waits, ready)
	}
}

func TestPollRetriesWhenReadinessIsConsumedBeforeRecheck(t *testing.T) {
	waits := 0
	ready := false
	p := pollableValue{
		ready: func() bool { return ready },
		wait: func(context.Context) error {
			waits++
			if waits == 2 {
				ready = true
			}
			return nil
		},
	}
	indices, err := waitPollables(context.Background(), []pollableValue{p})
	if err != nil {
		t.Fatal(err)
	}
	if waits != 2 || len(indices) != 1 || indices[0] != uint32(0) {
		t.Fatalf("poll returned indices %v after %d waits; want [0] after two waits", indices, waits)
	}
}

func TestTransientPollWaitsBackOffAndRemainCancellable(t *testing.T) {
	for _, operation := range testPollOperations() {
		t.Run(operation.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer cancel()
			var waits atomic.Int32
			p := pollableValue{
				ready: func() bool { return false },
				wait: func(context.Context) error {
					waits.Add(1)
					return nil
				},
			}
			if err := operation.run(ctx, p); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("%s returned %v, want context deadline", operation.name, err)
			}
			if count := waits.Load(); count > 100 {
				t.Fatalf("%s retried %d times in 40ms; want bounded retries", operation.name, count)
			}
		})
	}
}
