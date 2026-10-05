package p2_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wago-org/wasi/p2"
)

type neverWritableOutput struct{ checks int }

func (out *neverWritableOutput) CheckWrite() (uint64, error) {
	out.checks++
	return 0, nil
}
func (*neverWritableOutput) TryWrite([]byte) error { panic("write without permit") }
func (*neverWritableOutput) BeginFlush() error     { return nil }
func (*neverWritableOutput) WaitWritable(ctx context.Context) error {
	return ctx.Err()
}

func TestBlockingWriteRepeatedLostReadinessBacksOff(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	out := &neverWritableOutput{}
	_, err := callBlockingOutput(t, p2.Config{Stdout: out}, 0, ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocking write error=%v, want context deadline", err)
	}
	if out.checks > 100 {
		t.Fatalf("blocking write probed %d times in 40ms; want bounded retries", out.checks)
	}
}
