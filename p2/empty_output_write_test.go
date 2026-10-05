package p2_test

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	component "github.com/wago-org/component-model"
	"github.com/wago-org/wasi/p2"
)

// Rebuild: wasm-tools parse testdata/empty_output_write.wat -o testdata/empty_output_write.component.wasm
//
//go:embed testdata/empty_output_write.component.wasm
var emptyOutputWriteComponent []byte

type emptyWriteBackend struct {
	started, release chan struct{}
	flushGate        bool
	err              error
	writes, flushes  atomic.Int32
}

func (w *emptyWriteBackend) Write(p []byte) (int, error) {
	w.writes.Add(1)
	if w.started != nil && !w.flushGate {
		close(w.started)
		<-w.release
	}
	if w.err != nil {
		return 0, w.err
	}
	return len(p), nil
}
func (w *emptyWriteBackend) Flush() error {
	w.flushes.Add(1)
	if w.started != nil && w.flushGate {
		close(w.started)
		<-w.release
	}
	return w.err
}

func withEmptyOutput(t testing.TB, out p2.OutputStream, fn func(*component.Instance) error) {
	t.Helper()
	_, ref := optionsTestService(t)
	err := ref.With(func(service component.Service) error {
		return service.WithInstance(context.Background(), emptyOutputWriteComponent, fn, p2.Options(p2.Config{Stdout: out})...)
	})
	if err != nil {
		t.Fatal(err)
	}
}
func grantEmptyOutput(in *component.Instance) (uint64, error) {
	values, err := in.Call(context.Background(), "grant")
	if err != nil {
		return 0, err
	}
	return values[0].(uint64), nil
}
func emptyWriteStatus(in *component.Instance, op string, length uint32) (uint32, error) {
	values, err := in.Call(context.Background(), op, length)
	if err != nil {
		return 0, err
	}
	return values[0].(uint32), nil
}
func startEmptyWriteBackend(t *testing.T, flush bool, failure error) (*emptyWriteBackend, p2.OutputStream, func()) {
	t.Helper()
	w := &emptyWriteBackend{started: make(chan struct{}), release: make(chan struct{}), flushGate: flush, err: failure}
	out := p2.NewOutputStream(w)
	var err error
	if flush {
		err = out.BeginFlush()
	} else {
		err = out.TryWrite([]byte("pending"))
	}
	if err != nil {
		t.Fatal(err)
	}
	<-w.started
	released := false
	release := func() {
		if !released {
			close(w.release)
			released = true
		}
		out.WaitWritable(context.Background())
	}
	t.Cleanup(release)
	return w, out, release
}

func TestEmptyOutputWriteWithZeroPermit(t *testing.T) {
	for _, op := range []string{"write", "zeroes"} {
		for _, flush := range []bool{false, true} {
			name := "pending-write"
			if flush {
				name = "pending-flush"
			}
			t.Run(op+"/"+name, func(t *testing.T) {
				w, out, _ := startEmptyWriteBackend(t, flush, nil)
				withEmptyOutput(t, out, func(in *component.Instance) error {
					permit, err := grantEmptyOutput(in)
					if err != nil {
						return err
					}
					if permit != 0 {
						return fmt.Errorf("busy permit=%d, want 0", permit)
					}
					status, err := emptyWriteStatus(in, op, 0)
					if err != nil || status != 0 {
						return fmt.Errorf("valid empty write=%d,%v, want success", status, err)
					}
					_, err = emptyWriteStatus(in, op, 0)
					if err == nil || !strings.Contains(err.Error(), "permit") {
						return fmt.Errorf("reused empty-write permit=%v, want trap", err)
					}
					return nil
				})
				wantWrites, wantFlushes := int32(1), int32(0)
				if flush {
					wantWrites, wantFlushes = 0, 1
				}
				if w.writes.Load() != wantWrites || w.flushes.Load() != wantFlushes {
					t.Fatal("empty operation reached underlying writer/flusher")
				}
			})
		}
	}
}
func TestEmptyOutputWriteDoesNotCallIdleWriter(t *testing.T) {
	for _, op := range []string{"write", "zeroes"} {
		t.Run(op, func(t *testing.T) {
			w := &emptyWriteBackend{}
			out := p2.NewOutputStream(w)
			withEmptyOutput(t, out, func(in *component.Instance) error {
				if _, err := grantEmptyOutput(in); err != nil {
					return err
				}
				status, err := emptyWriteStatus(in, op, 0)
				if err != nil || status != 0 {
					return fmt.Errorf("empty output=%d,%v", status, err)
				}
				return out.WaitWritable(context.Background())
			})
			if w.writes.Load() != 0 || w.flushes.Load() != 0 {
				t.Fatal("empty output called underlying I/O")
			}
		})
	}
}
func TestEmptyOutputWriteRequiresPermit(t *testing.T) {
	for _, op := range []string{"write", "zeroes"} {
		t.Run(op, func(t *testing.T) {
			w := &emptyWriteBackend{}
			withEmptyOutput(t, p2.NewOutputStream(w), func(in *component.Instance) error {
				_, err := emptyWriteStatus(in, op, 0)
				if err == nil || !strings.Contains(err.Error(), "permit") {
					return fmt.Errorf("missing permit=%v", err)
				}
				return nil
			})
			if w.writes.Load() != 0 {
				t.Fatal("unpermitted empty write reached writer")
			}
		})
	}
}
func TestEmptyOutputWritePreservesFailureAfterGrant(t *testing.T) {
	for _, op := range []string{"write", "zeroes"} {
		for _, tc := range []struct {
			name    string
			failure error
			want    uint32
		}{{"closed", io.EOF, 257}, {"io-error", errors.New("write failed"), 1}} {
			t.Run(op+"/"+tc.name, func(t *testing.T) {
				_, out, release := startEmptyWriteBackend(t, false, tc.failure)
				withEmptyOutput(t, out, func(in *component.Instance) error {
					permit, err := grantEmptyOutput(in)
					if err != nil {
						return err
					}
					if permit != 0 {
						return fmt.Errorf("busy permit=%d", permit)
					}
					release()
					status, err := emptyWriteStatus(in, op, 0)
					if err != nil || status != tc.want {
						return fmt.Errorf("failure after grant=%d,%v, want%d", status, err, tc.want)
					}
					return nil
				})
			})
		}
	}
}

func BenchmarkEmptyOutputCanonicalWrite(b *testing.B) {
	for _, op := range []string{"write", "zeroes"} {
		b.Run(op, func(b *testing.B) {
			out := p2.NewOutputStream(io.Discard)
			withEmptyOutput(b, out, func(in *component.Instance) error {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := grantEmptyOutput(in); err != nil {
						return err
					}
					status, err := emptyWriteStatus(in, op, 0)
					if err != nil || status != 0 {
						return fmt.Errorf("empty output=%d,%v", status, err)
					}
					if err := out.WaitWritable(context.Background()); err != nil {
						return err
					}
				}
				b.StopTimer()
				return nil
			})
		})
	}
}
