package p2_test

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"testing"

	component "github.com/wago-org/component-model"
	"github.com/wago-org/wasi/p2"
)

// Rebuild: wasm-tools parse p2/testdata/blocking_input_errors.wat -o p2/testdata/blocking_input_errors.component.wasm
//
//go:embed testdata/blocking_input_errors.component.wasm
var blockingInputErrorsComponent []byte

type readinessFailureInput struct {
	err    error
	cancel context.CancelFunc
	reads  int
	waits  int
}

func (in *readinessFailureInput) TryRead(dst []byte) (int, error) {
	in.reads++
	dst[0] = 'x'
	return 1, nil
}
func (in *readinessFailureInput) WaitReadable(ctx context.Context) error {
	in.waits++
	if in.cancel != nil {
		in.cancel()
		return fmt.Errorf("input: %w", ctx.Err())
	}
	return in.err
}
func TestBlockingInputCachesClosedWaitResult(t *testing.T) {
	input := &readinessFailureInput{err: fmt.Errorf("input: %w", io.EOF)}
	err := withBlockingInput(t, context.Background(), input, func(in *component.Instance) error {
		for _, method := range []string{"read", "skip", "read", "skip"} {
			values, err := in.Call(context.Background(), method, uint64(1))
			if err != nil {
				return fmt.Errorf("blocking %s trapped after EOF: %w", method, err)
			}
			if got := values[0].(uint32); got != 1 {
				return fmt.Errorf("blocking %s status=%d, want closed", method, got)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.waits != 1 || input.reads != 0 {
		t.Fatalf("closed input waits=%d reads=%d, want one wait and no read", input.waits, input.reads)
	}
}
func withBlockingInput(tb testing.TB, ctx context.Context, input p2.InputStream, fn func(*component.Instance) error) error {
	tb.Helper()
	_, ref := optionsTestService(tb)
	return ref.With(func(service component.Service) error {
		return service.WithInstance(ctx, blockingInputErrorsComponent, fn, p2.Options(p2.Config{Stdin: input})...)
	})
}
func TestBlockingInputReturnsWaitErrors(t *testing.T) {
	for _, method := range []string{"read", "skip"} {
		for _, tc := range []struct {
			name string
			err  error
			want uint32
		}{
			{"io", io.ErrClosedPipe, 2},
			{"closed", io.EOF, 1},
			{"wrapped-closed", fmt.Errorf("input: %w", io.EOF), 1},
			{"context-sentinel", context.Canceled, 2},
			{"wrapped-context-sentinel", fmt.Errorf("input: %w", context.DeadlineExceeded), 2},
			{"ready", nil, 0},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				input := &readinessFailureInput{err: tc.err}
				err := withBlockingInput(t, context.Background(), input, func(in *component.Instance) error {
					values, err := in.Call(context.Background(), method, uint64(1))
					if err != nil {
						return fmt.Errorf("blocking %s trapped instead of stream-error: %w", method, err)
					}
					if got := values[0].(uint32); got != tc.want {
						return fmt.Errorf("blocking %s status=%d, want %d", method, got, tc.want)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				wantReads := 0
				if tc.err == nil {
					wantReads = 1
				}
				if input.reads != wantReads {
					t.Fatalf("reads=%d,want%d", input.reads, wantReads)
				}
			})
		}
	}
}
func TestBlockingInputPreservesCallerCancellation(t *testing.T) {
	for _, method := range []string{"read", "skip"} {
		t.Run(method, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			input := &readinessFailureInput{cancel: cancel}
			err := withBlockingInput(t, ctx, input, func(in *component.Instance) error {
				_, err := in.Call(ctx, method, uint64(1))
				if !errors.Is(err, context.Canceled) {
					return fmt.Errorf("blocking %s cancellation=%v", method, err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if input.reads != 0 {
				t.Fatalf("canceled wait consumed input")
			}
		})
	}
}
func BenchmarkBlockingInputReady(b *testing.B) {
	input := &readinessFailureInput{}
	err := withBlockingInput(b, context.Background(), input, func(in *component.Instance) error {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			values, err := in.Call(context.Background(), "read", uint64(1))
			if err != nil {
				return err
			}
			if values[0].(uint32) != 0 {
				return fmt.Errorf("ready input returned an error")
			}
		}
		b.StopTimer()
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
}
