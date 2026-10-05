package p2_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wago-org/wasi/p2"
)

type transientSpliceInput struct{ reads int }

func (*transientSpliceInput) WaitReadable(context.Context) error { return nil }
func (in *transientSpliceInput) TryRead(dst []byte) (int, error) {
	in.reads++
	if in.reads == 1 {
		return 0, p2.ErrWouldBlock
	}
	return copy(dst, "abc"), nil
}

type transientSpliceOutput struct {
	checks int
	data   []byte
}

func (out *transientSpliceOutput) CheckWrite() (uint64, error) {
	out.checks++
	if out.checks == 1 {
		return 0, nil
	}
	return 3, nil
}
func (out *transientSpliceOutput) TryWrite(p []byte) error {
	out.data = append(out.data, p...)
	return nil
}
func (*transientSpliceOutput) BeginFlush() error                  { return nil }
func (*transientSpliceOutput) WaitWritable(context.Context) error { return nil }

func TestBlockingSpliceRetriesLostOutputReadiness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	in := &spliceInput{gate: newSpliceGate(true), data: []byte("abc")}
	out := &transientSpliceOutput{}
	got, err := callSplice(t, p2.Config{Stdin: in, Stdout: out}, ctx, 1, 3)
	if err != nil || got != 3 || string(out.data) != "abc" || out.checks != 2 {
		t.Fatalf("blocking splice = %d, %v, output=%q, checks=%d; want 3, nil, abc, 2", got, err, out.data, out.checks)
	}
}

func TestBlockingSpliceRetriesLostInputReadiness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	in := &transientSpliceInput{}
	out := &spliceOutput{gate: newSpliceGate(true)}
	got, err := callSplice(t, p2.Config{Stdin: in, Stdout: out}, ctx, 1, 3)
	if err != nil || got != 3 || string(out.data) != "abc" || in.reads != 2 {
		t.Fatalf("blocking splice = %d, %v, output=%q, reads=%d; want 3, nil, abc, 2", got, err, out.data, in.reads)
	}
}

type neverWritableSpliceOutput struct{ checks int }

func (out *neverWritableSpliceOutput) CheckWrite() (uint64, error) {
	out.checks++
	return 0, nil
}
func (*neverWritableSpliceOutput) TryWrite([]byte) error { panic("write without permit") }
func (*neverWritableSpliceOutput) BeginFlush() error     { return nil }
func (*neverWritableSpliceOutput) WaitWritable(ctx context.Context) error {
	return ctx.Err()
}

func TestBlockingSpliceRepeatedLostReadinessBacksOff(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	in := &spliceInput{gate: newSpliceGate(true), data: []byte("abc")}
	out := &neverWritableSpliceOutput{}
	_, err := callSplice(t, p2.Config{Stdin: in, Stdout: out}, ctx, 1, 3)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocking splice error=%v, want caller deadline", err)
	}
	if out.checks > 100 {
		t.Fatalf("blocking splice probed %d times in 40ms; want bounded retries", out.checks)
	}
}
