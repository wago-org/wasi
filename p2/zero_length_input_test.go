package p2_test

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"sync/atomic"
	"testing"

	component "github.com/wago-org/component-model"
	"github.com/wago-org/wasi/p2"
)

// Rebuild: wasm-tools parse testdata/zero_length_input.wat -o testdata/zero_length_input.component.wasm
//
//go:embed testdata/zero_length_input.component.wasm
var zeroLengthInputComponent []byte

type zeroLengthInput struct {
	remaining                int
	zeroCalls, positiveCalls int
}

func (s *zeroLengthInput) TryRead(p []byte) (int, error) {
	if len(p) == 0 {
		s.zeroCalls++
		return 0, nil
	}
	s.positiveCalls++
	if s.remaining == 0 {
		return 0, io.EOF
	}
	p[0] = 'x'
	s.remaining--
	return 1, nil
}
func (*zeroLengthInput) WaitReadable(context.Context) error { return nil }

func withZeroLengthInput(t testing.TB, input p2.InputStream, fn func(*component.Instance) error) {
	t.Helper()
	_, ref := optionsTestService(t)
	err := ref.With(func(s component.Service) error {
		return s.WithInstance(context.Background(), zeroLengthInputComponent, fn, p2.Options(p2.Config{Stdin: input})...)
	})
	if err != nil {
		t.Fatal(err)
	}
}
func inputStatus(in *component.Instance, name string, n uint64) (uint32, error) {
	values, err := in.Call(context.Background(), name, n)
	if err != nil {
		return 0, err
	}
	if len(values) != 1 {
		return 0, fmt.Errorf("%s result=%v", name, values)
	}
	result, ok := values[0].(uint32)
	if !ok {
		return 0, fmt.Errorf("%s result type=%T", name, values[0])
	}
	return result, nil
}
func expectInputStatus(in *component.Instance, name string, n uint64, want uint32) error {
	got, err := inputStatus(in, name, n)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%s(%d) status=%d, want%d (0=success,1=closed)", name, n, got, want)
	}
	return nil
}
func TestZeroLengthInputClosed(t *testing.T) {
	for _, name := range []string{"read", "skip"} {
		t.Run("initially-closed-"+name, func(t *testing.T) {
			withZeroLengthInput(t, nil, func(in *component.Instance) error { return expectInputStatus(in, name, 0, 1) })
		})
	}
	t.Run("observed-EOF", func(t *testing.T) {
		input := &zeroLengthInput{}
		withZeroLengthInput(t, input, func(in *component.Instance) error {
			if err := expectInputStatus(in, "read", 1, 1); err != nil {
				return err
			}
			for _, name := range []string{"read", "skip"} {
				if err := expectInputStatus(in, name, 0, 1); err != nil {
					return err
				}
			}
			if input.zeroCalls != 0 {
				return fmt.Errorf("closed stream was probed %d times", input.zeroCalls)
			}
			return nil
		})
	})
}
func TestZeroLengthInputOpen(t *testing.T) {
	input := &zeroLengthInput{remaining: 1}
	withZeroLengthInput(t, input, func(in *component.Instance) error {
		for _, name := range []string{"read", "skip"} {
			if err := expectInputStatus(in, name, 0, 0); err != nil {
				return err
			}
		}
		if input.remaining != 1 || input.positiveCalls != 0 {
			return fmt.Errorf("zero requests consumed input")
		}
		if err := expectInputStatus(in, "read", 1, 0); err != nil {
			return err
		}
		if input.remaining != 0 {
			return fmt.Errorf("data read did not consume byte")
		}
		if err := expectInputStatus(in, "read", 1, 1); err != nil {
			return err
		}
		return expectInputStatus(in, "skip", 0, 1)
	})
}

type zeroLengthReader struct{ calls atomic.Int32 }

func (s *zeroLengthReader) Read(p []byte) (int, error) { s.calls.Add(1); return 0, io.EOF }
func TestZeroLengthInputDoesNotStartAsyncRead(t *testing.T) {
	reader := &zeroLengthReader{}
	withZeroLengthInput(t, p2.NewInputStream(reader), func(in *component.Instance) error {
		for i := 0; i < 8; i++ {
			for _, name := range []string{"read", "skip"} {
				if err := expectInputStatus(in, name, 0, 0); err != nil {
					return err
				}
			}
		}
		if reader.calls.Load() != 0 {
			return fmt.Errorf("zero requests started underlying read")
		}
		return nil
	})
}
func BenchmarkZeroLengthInput(b *testing.B) {
	for _, closed := range []bool{false, true} {
		name := "open"
		if closed {
			name = "closed"
		}
		b.Run(name, func(b *testing.B) {
			var input p2.InputStream
			if !closed {
				input = &zeroLengthInput{remaining: 1}
			}
			withZeroLengthInput(b, input, func(in *component.Instance) error {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := inputStatus(in, "read", 0); err != nil {
						return err
					}
				}
				b.StopTimer()
				return nil
			})
		})
	}
}
