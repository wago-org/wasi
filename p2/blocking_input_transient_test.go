package p2_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	component "github.com/wago-org/component-model"
	"github.com/wago-org/wasi/p2"
)

// Readiness may be consumed between the wait and the actual read. A blocking
// operation must try again instead of returning an empty successful result.
type transientBlockingInput struct{ waits, reads int }

func (in *transientBlockingInput) WaitReadable(context.Context) error {
	in.waits++
	return nil
}

type neverReadyBlockingInput struct{ waits int }

func (in *neverReadyBlockingInput) WaitReadable(ctx context.Context) error {
	in.waits++
	return ctx.Err()
}

func (*neverReadyBlockingInput) TryRead([]byte) (int, error) { return 0, p2.ErrWouldBlock }

func TestBlockingInputRepeatedLostReadinessBacksOff(t *testing.T) {
	input := &neverReadyBlockingInput{}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	err := withBlockingInput(t, ctx, input, func(in *component.Instance) error {
		_, err := in.Call(ctx, "read", uint64(1))
		if !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("blocking read error=%v, want context deadline", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.waits > 100 {
		t.Fatalf("blocking read retried %d times in 40ms; want bounded retries", input.waits)
	}
}

func (in *transientBlockingInput) TryRead(dst []byte) (int, error) {
	in.reads++
	if in.reads == 1 {
		return 0, p2.ErrWouldBlock
	}
	dst[0] = 'x'
	return 1, nil
}

func TestBlockingInputRetriesAfterTransientReadiness(t *testing.T) {
	for _, method := range []string{"read", "skip"} {
		t.Run(method, func(t *testing.T) {
			input := &transientBlockingInput{}
			err := withBlockingInput(t, context.Background(), input, func(in *component.Instance) error {
				values, err := in.Call(context.Background(), method, uint64(1))
				if err != nil {
					return err
				}
				if status := values[0].(uint32); status != 0 {
					return fmt.Errorf("blocking %s status=%d, want success", method, status)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if input.waits != 2 || input.reads != 2 {
				t.Fatalf("blocking %s waits=%d reads=%d, want two of each", method, input.waits, input.reads)
			}
		})
	}
}
