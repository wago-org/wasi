package p2_test

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	component "github.com/wago-org/component-model"
	"github.com/wago-org/wasi/p2"
)

//go:embed testdata/blocking_output_errors.component.wasm
var blockingOutputErrorsComponent []byte

type failingOutputWriter struct {
	writeErr, flushErr error
}

func (w failingOutputWriter) Write(p []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return len(p), nil
}
func (w failingOutputWriter) Flush() error { return w.flushErr }

func callBlockingOutput(t *testing.T, cfg p2.Config, operation uint32, ctx context.Context) (uint32, error) {
	t.Helper()
	_, ref := optionsTestService(t)
	var result uint32
	err := ref.With(func(service component.Service) error {
		return service.WithInstance(ctx, blockingOutputErrorsComponent, func(in *component.Instance) error {
			values, err := in.Call(ctx, "run", operation)
			if err != nil {
				return err
			}
			result = values[0].(uint32)
			return nil
		}, p2.Options(cfg)...)
	})
	return result, err
}

func TestBlockingOutputReturnsStreamErrors(t *testing.T) {
	for operation, name := range []string{"write-and-flush", "write-zeroes-and-flush", "flush"} {
		for _, tc := range []struct {
			name string
			err  error
			want uint32
		}{
			{"io", errors.New("output unavailable"), 1},
			{"context-sentinel", context.Canceled, 1},
			{"wrapped-context-sentinel", fmt.Errorf("write: %w", context.DeadlineExceeded), 1},
			{"closed", io.EOF, 1 | 1<<8},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				writer := failingOutputWriter{writeErr: tc.err}
				if operation == 2 {
					writer = failingOutputWriter{flushErr: tc.err}
				}
				got, err := callBlockingOutput(t, p2.Config{Stdout: p2.NewOutputStream(writer)}, uint32(operation), context.Background())
				if err != nil {
					t.Fatalf("stream failure trapped: %v; want typed stream-error", err)
				}
				if got != tc.want {
					t.Fatalf("result/stream-error discriminators = %#x, want %#x", got, tc.want)
				}
			})
		}
		if operation != 2 {
			t.Run(name+"/flush-after-write", func(t *testing.T) {
				got, err := callBlockingOutput(t, p2.Config{Stdout: p2.NewOutputStream(failingOutputWriter{flushErr: errors.New("flush failed")})}, uint32(operation), context.Background())
				if err != nil || got != 1 {
					t.Fatalf("flush failure = %#x, %v; want typed last-operation-failed", got, err)
				}
			})
		}
		t.Run(name+"/success", func(t *testing.T) {
			got, err := callBlockingOutput(t, p2.Config{Stdout: p2.NewOutputStream(failingOutputWriter{})}, uint32(operation), context.Background())
			if err != nil || got != 0 {
				t.Fatalf("successful output = %#x, %v", got, err)
			}
		})
	}
}

type canceledOutputWriter struct {
	started, release chan struct{}
	flush            bool
}

func (w canceledOutputWriter) Write(p []byte) (int, error) {
	if !w.flush {
		close(w.started)
		<-w.release
	}
	return len(p), nil
}
func (w canceledOutputWriter) Flush() error {
	if w.flush {
		close(w.started)
		<-w.release
	}
	return nil
}

func TestBlockingOutputPreservesCallerCancellation(t *testing.T) {
	for operation, name := range []string{"write-and-flush", "write-zeroes-and-flush", "flush"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			writer := canceledOutputWriter{started: make(chan struct{}), release: make(chan struct{}), flush: operation == 2}
			out := p2.NewOutputStream(writer)
			go func() { <-writer.started; cancel() }()
			_, err := callBlockingOutput(t, p2.Config{Stdout: out}, uint32(operation), ctx)
			close(writer.release)
			// Drain the caller-owned adapter before the test exits.
			if waitErr := out.WaitWritable(context.Background()); waitErr != nil {
				t.Fatal(waitErr)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("caller cancellation = %v, want context.Canceled", err)
			}
		})
	}
}

// A provided stream can report flush completion as readiness and expose the
// completed operation's failure through CheckWrite, just like a pollable.
type readinessErrorOutput struct{ flushed bool }

func (o *readinessErrorOutput) CheckWrite() (uint64, error) {
	if o.flushed {
		return 0, errors.New("completed flush failed")
	}
	return 4096, nil
}
func (*readinessErrorOutput) TryWrite([]byte) error              { return nil }
func (o *readinessErrorOutput) BeginFlush() error                { o.flushed = true; return nil }
func (*readinessErrorOutput) WaitWritable(context.Context) error { return nil }

func TestBlockingOutputChecksCompletedFlushError(t *testing.T) {
	for operation, name := range []string{"write-and-flush", "write-zeroes-and-flush", "flush"} {
		t.Run(name, func(t *testing.T) {
			got, err := callBlockingOutput(t, p2.Config{Stdout: &readinessErrorOutput{}}, uint32(operation), context.Background())
			if err != nil || got != 1 {
				t.Fatalf("completed flush = %#x, %v; want typed last-operation-failed", got, err)
			}
		})
	}
}

// CheckWrite grants a smaller capacity after flush. The host must replace the
// earlier grant even when the blocking operation itself writes no bytes.
type shrinkingOutput struct {
	err     error
	flushed bool
	after   uint64
	writes  int
	bytes   int
}

func (o *shrinkingOutput) CheckWrite() (uint64, error) {
	if o.flushed {
		return o.after, o.err
	}
	return 64, nil
}
func (o *shrinkingOutput) TryWrite(p []byte) error          { o.writes++; o.bytes += len(p); return nil }
func (o *shrinkingOutput) BeginFlush() error                { o.flushed = true; return nil }
func (*shrinkingOutput) WaitWritable(context.Context) error { return nil }

func TestBlockingOutputRefreshesWritePermit(t *testing.T) {
	for operation, name := range []string{"empty-write-and-flush", "empty-write-zeroes-and-flush", "flush"} {
		for _, tc := range []struct {
			name       string
			capacity   uint64
			length     uint32
			rejected   bool
			probeError bool
		}{
			{"shrinking", 3, 4, true, false},
			{"zero-capacity", 0, 1, true, false},
			{"current-capacity", 3, 3, false, false},
			{"io-limit", 4096, 33, true, false},
			{"probe-error", 0, 1, true, true},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				_, ref := optionsTestService(t)
				out := &shrinkingOutput{after: tc.capacity}
				if tc.probeError {
					out.err = errors.New("probe failed")
				}
				err := ref.With(func(service component.Service) error {
					return service.WithInstance(context.Background(), blockingOutputErrorsComponent, func(in *component.Instance) error {
						_, err := in.Call(context.Background(), "flush-and-write", uint32(operation), tc.length)
						if tc.rejected {
							if err == nil || !strings.Contains(err.Error(), "permit") {
								return fmt.Errorf("write exceeding current permit = %v, want permit trap", err)
							}
							if out.writes != 0 {
								return fmt.Errorf("stale permit reached writer: %d writes/%d bytes", out.writes, out.bytes)
							}
							return nil
						}
						if err == nil && (out.writes != 1 || out.bytes != int(tc.length)) {
							return fmt.Errorf("current permit write = %d writes/%d bytes", out.writes, out.bytes)
						}
						return err
					}, p2.Options(p2.Config{Stdout: out, Limits: p2.Limits{MaxAggregateBufferBytes: 32}})...)
				})
				if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
