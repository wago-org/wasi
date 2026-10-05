package p2_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	component "github.com/wago-org/component-model"
	"github.com/wago-org/wasi/p2"
)

func TestGuestOutputClosesAfterReportedFailure(t *testing.T) {
	for _, hostWaited := range []bool{false, true} {
		t.Run(fmt.Sprint("host-waited=", hostWaited), func(t *testing.T) {
			out := p2.NewOutputStream(failingOutputWriter{flushErr: errors.New("flush failed")})
			if hostWaited {
				if err := out.BeginFlush(); err != nil {
					t.Fatal(err)
				}
				if err := out.WaitWritable(context.Background()); err == nil {
					t.Fatal("host wait did not observe flush failure")
				}
			}
			_, ref := optionsTestService(t)
			err := ref.With(func(service component.Service) error {
				return service.WithInstance(context.Background(), blockingOutputErrorsComponent, func(in *component.Instance) error {
					for i, want := range []uint32{1, 257} {
						values, err := in.Call(context.Background(), "run", uint32(2))
						if err != nil {
							return err
						}
						if got := values[0].(uint32); got != want {
							return fmt.Errorf("guest flush %d status=%d, want %d", i+1, got, want)
						}
					}
					return nil
				}, p2.Options(p2.Config{Stdout: out})...)
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
