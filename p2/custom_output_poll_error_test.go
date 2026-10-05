package p2_test

import (
	"context"
	_ "embed"
	"io"
	"testing"

	component "github.com/wago-org/component-model"
	"github.com/wago-org/wasi/p2"
)

//go:embed testdata/custom_output_poll_error.component.wasm
var customOutputPollErrorComponent []byte

type oneShotPollFailure struct{ checks int }

func (out *oneShotPollFailure) CheckWrite() (uint64, error) {
	out.checks++
	if out.checks == 1 {
		return 0, io.ErrClosedPipe
	}
	return 0, io.EOF
}
func (*oneShotPollFailure) TryWrite([]byte) error              { panic("unexpected write") }
func (*oneShotPollFailure) BeginFlush() error                  { panic("unexpected flush") }
func (*oneShotPollFailure) WaitWritable(context.Context) error { return nil }

func TestCustomOutputPollPreservesFirstFailureForGuest(t *testing.T) {
	_, ref := optionsTestService(t)
	out := &oneShotPollFailure{}
	err := ref.With(func(service component.Service) error {
		return service.WithInstance(context.Background(), customOutputPollErrorComponent, func(in *component.Instance) error {
			values, err := in.Call(context.Background(), "run")
			if err != nil {
				return err
			}
			if got := values[0].(uint32); got != 1 {
				t.Errorf("check-write after pollable.ready = %d, want last-operation-failed (1)", got)
			}
			return nil
		}, p2.Options(p2.Config{Stdout: out})...)
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.checks != 1 {
		t.Fatalf("underlying CheckWrite calls = %d, want one poll probe", out.checks)
	}
}

type oneShotPollPermit struct{ checks int }

func (out *oneShotPollPermit) CheckWrite() (uint64, error) {
	out.checks++
	if out.checks == 1 {
		return 7, nil
	}
	return 0, nil
}
func (*oneShotPollPermit) TryWrite([]byte) error              { panic("unexpected write") }
func (*oneShotPollPermit) BeginFlush() error                  { panic("unexpected flush") }
func (*oneShotPollPermit) WaitWritable(context.Context) error { return nil }

func TestCustomOutputPollPreservesFirstPermitForGuest(t *testing.T) {
	_, ref := optionsTestService(t)
	out := &oneShotPollPermit{}
	err := ref.With(func(service component.Service) error {
		return service.WithInstance(context.Background(), customOutputPollErrorComponent, func(in *component.Instance) error {
			values, err := in.Call(context.Background(), "run")
			if err != nil {
				return err
			}
			if got := values[0].(uint32) & 0xff; got != 0 {
				t.Errorf("check-write after pollable.ready status = %d, want success", got)
			}
			values, err = in.Call(context.Background(), "permit-value")
			if err != nil {
				return err
			}
			if got := values[0].(uint64); got != 7 {
				t.Errorf("check-write after pollable.ready permit = %d, want 7", got)
			}
			return nil
		}, p2.Options(p2.Config{Stdout: out})...)
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.checks != 1 {
		t.Fatalf("underlying CheckWrite calls = %d, want one poll probe", out.checks)
	}
}
