package p2_test

import (
	"context"
	"io"
	"testing"

	component "github.com/wago-org/component-model"
)

type failingThenReadableInput struct {
	reads    int
	withByte bool
}

func (*failingThenReadableInput) WaitReadable(context.Context) error { return nil }
func (in *failingThenReadableInput) TryRead(dst []byte) (int, error) {
	in.reads++
	if in.reads == 1 {
		if in.withByte {
			dst[0] = 'x'
			return 1, io.ErrClosedPipe
		}
		return 0, io.ErrClosedPipe
	}
	dst[0] = 'x'
	return 1, nil
}

func TestInputStreamClosesAfterLastOperationFailed(t *testing.T) {
	for _, withByte := range []bool{false, true} {
		input := &failingThenReadableInput{withByte: withByte}
		withZeroLengthInput(t, input, func(in *component.Instance) error {
			if withByte {
				if err := expectInputStatus(in, "read", 1, 0); err != nil {
					return err
				}
			}
			if err := expectInputStatus(in, "read", 1, 2); err != nil {
				return err
			}
			if err := expectInputStatus(in, "read", 1, 1); err != nil {
				return err
			}
			return expectInputStatus(in, "read", 0, 1)
		})
		if input.reads != 1 {
			t.Fatalf("closed input with byte=%t made %d host reads, want one", withByte, input.reads)
		}
	}
}
