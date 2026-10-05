package p2_test

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	component "github.com/wago-org/component-model"
	"github.com/wago-org/wasi/p2"
)

//go:embed testdata/blocking_splice.component.wasm
var blockingSpliceComponent []byte

const spliceErrorResult uint64 = 1 << 63

func withSpliceInstance(tb testing.TB, ctx context.Context, cfg p2.Config, call func(*component.Instance) error) error {
	tb.Helper()
	_, ref := optionsTestService(tb)
	return ref.With(func(service component.Service) error {
		return service.WithInstance(ctx, blockingSpliceComponent, func(in *component.Instance) error {
			if _, err := in.Call(ctx, "init"); err != nil {
				return err
			}
			return call(in)
		}, p2.Options(cfg)...)
	})
}

func invokeSplice(in *component.Instance, ctx context.Context, blocking uint32, length uint64) (uint64, error) {
	values, err := in.Call(ctx, "run", blocking, length)
	if err != nil {
		return 0, err
	}
	return values[0].(uint64), nil
}

func callSplice(tb testing.TB, cfg p2.Config, ctx context.Context, blocking uint32, length uint64) (uint64, error) {
	tb.Helper()
	var result uint64
	err := withSpliceInstance(tb, ctx, cfg, func(in *component.Instance) error {
		var err error
		result, err = invokeSplice(in, ctx, blocking, length)
		return err
	})
	return result, err
}

type spliceGate struct {
	ready, entered chan struct{}
	releaseOnce    sync.Once
	enteredOnce    sync.Once
	waits          atomic.Int32
}

func newSpliceGate(ready bool) *spliceGate {
	gate := &spliceGate{ready: make(chan struct{}), entered: make(chan struct{})}
	if ready {
		gate.release()
	}
	return gate
}
func (g *spliceGate) release() { g.releaseOnce.Do(func() { close(g.ready) }) }
func (g *spliceGate) isReady() bool {
	select {
	case <-g.ready:
		return true
	default:
		return false
	}
}
func (g *spliceGate) wait(ctx context.Context) error {
	g.waits.Add(1)
	g.enteredOnce.Do(func() { close(g.entered) })
	select {
	case <-g.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type spliceInput struct {
	gate             *spliceGate
	waitErr, readErr error
	data             []byte
	reads            atomic.Int32
}

func (in *spliceInput) TryRead(dst []byte) (int, error) {
	in.reads.Add(1)
	if in.readErr != nil {
		return 0, in.readErr
	}
	if !in.gate.isReady() {
		return 0, p2.ErrWouldBlock
	}
	n := copy(dst, in.data)
	in.data = in.data[n:]
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}
func (in *spliceInput) WaitReadable(ctx context.Context) error {
	if in.waitErr != nil {
		return in.waitErr
	}
	return in.gate.wait(ctx)
}

type spliceOutput struct {
	gate                        *spliceGate
	waitErr, probeErr, writeErr error
	data                        []byte
}

func (out *spliceOutput) CheckWrite() (uint64, error) {
	if out.probeErr != nil {
		return 0, out.probeErr
	}
	if !out.gate.isReady() {
		return 0, nil
	}
	return 4096, nil
}
func (out *spliceOutput) TryWrite(p []byte) error {
	if out.writeErr != nil {
		return out.writeErr
	}
	if len(p) > 0 && !out.gate.isReady() {
		return errors.New("write before readiness")
	}
	out.data = append(out.data, p...)
	return nil
}
func (*spliceOutput) BeginFlush() error { return nil }
func (out *spliceOutput) WaitWritable(ctx context.Context) error {
	if out.waitErr != nil {
		return out.waitErr
	}
	return out.gate.wait(ctx)
}

func TestBlockingSpliceWaitsForBothStreams(t *testing.T) {
	inputGate, outputGate := newSpliceGate(false), newSpliceGate(false)
	defer inputGate.release()
	defer outputGate.release()
	input := &spliceInput{gate: inputGate, data: []byte("abc")}
	output := &spliceOutput{gate: outputGate}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := withSpliceInstance(t, ctx, p2.Config{Stdin: input, Stdout: output}, func(in *component.Instance) error {
		type result struct {
			n   uint64
			err error
		}
		done := make(chan result, 1)
		go func() { n, err := invokeSplice(in, ctx, 1, 3); done <- result{n, err} }()
		joined := false
		defer func() {
			cancel()
			if !joined {
				<-done
			}
		}()
		select {
		case <-outputGate.entered:
		case got := <-done:
			joined = true
			return fmt.Errorf("splice returned %d, %v before waiting for output", got.n, got.err)
		case <-ctx.Done():
			return errors.New("splice never waited for output")
		}
		if inputGate.waits.Load() != 0 || input.reads.Load() != 0 {
			return errors.New("splice accessed input before output was ready")
		}
		outputGate.release()
		select {
		case <-inputGate.entered:
		case got := <-done:
			joined = true
			return fmt.Errorf("splice returned %d, %v before waiting for input", got.n, got.err)
		case <-ctx.Done():
			return errors.New("splice never waited for input")
		}
		if input.reads.Load() != 0 {
			return errors.New("splice consumed input before readiness")
		}
		inputGate.release()
		got := <-done
		joined = true
		if got.err != nil || got.n != 3 || string(output.data) != "abc" {
			return fmt.Errorf("ready splice = %d, %v, %q; want 3, nil, abc", got.n, got.err, output.data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNonblockingSpliceDoesNotWait(t *testing.T) {
	inputGate, outputGate := newSpliceGate(false), newSpliceGate(false)
	input := &spliceInput{gate: inputGate, data: []byte("abc")}
	output := &spliceOutput{gate: outputGate}
	got, err := callSplice(t, p2.Config{Stdin: input, Stdout: output}, context.Background(), 0, 3)
	if err != nil || got != 0 || inputGate.waits.Load() != 0 || outputGate.waits.Load() != 0 || string(input.data) != "abc" {
		t.Fatalf("nonblocking splice = %d, %v; waits input=%d/output=%d, input=%q", got, err, inputGate.waits.Load(), outputGate.waits.Load(), input.data)
	}
}

func TestBlockingSplicePreservesCancellation(t *testing.T) {
	for _, waitInput := range []bool{false, true} {
		name := "output"
		if waitInput {
			name = "input"
		}
		t.Run(name, func(t *testing.T) {
			inputGate, outputGate := newSpliceGate(!waitInput), newSpliceGate(waitInput)
			defer inputGate.release()
			defer outputGate.release()
			waiting := outputGate
			if waitInput {
				waiting = inputGate
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			controllerDone := make(chan struct{})
			go func() {
				defer close(controllerDone)
				select {
				case <-waiting.entered:
					cancel()
				case <-ctx.Done():
				}
			}()
			got, err := callSplice(t, p2.Config{Stdin: &spliceInput{gate: inputGate, data: []byte("abc")}, Stdout: &spliceOutput{gate: outputGate}}, ctx, 1, 3)
			cancel()
			<-controllerDone
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled %s wait = %d, %v; want caller cancellation", name, got, err)
			}
		})
	}
}

func TestBlockingSpliceReturnsStreamErrors(t *testing.T) {
	for _, where := range []string{"output-wait", "input-wait", "output-probe", "input-read", "output-write"} {
		for _, tc := range []struct {
			name string
			err  error
			want uint64
		}{
			{"io", io.ErrClosedPipe, spliceErrorResult},
			{"context-sentinel", context.Canceled, spliceErrorResult},
			{"wrapped-context-sentinel", fmt.Errorf("I/O: %w", context.DeadlineExceeded), spliceErrorResult},
			{"closed", io.EOF, spliceErrorResult | 1},
		} {
			t.Run(where+"/"+tc.name, func(t *testing.T) {
				input := &spliceInput{gate: newSpliceGate(true), data: []byte("abc")}
				output := &spliceOutput{gate: newSpliceGate(true)}
				switch where {
				case "output-wait":
					output.waitErr = tc.err
				case "input-wait":
					input.waitErr = tc.err
				case "output-probe":
					output.probeErr = tc.err
				case "input-read":
					input.readErr = tc.err
				case "output-write":
					output.writeErr = tc.err
				}
				got, err := callSplice(t, p2.Config{Stdin: input, Stdout: output}, context.Background(), 1, 3)
				if err != nil || got != tc.want || len(output.data) != 0 {
					t.Fatalf("stream failure = %#x, %v, written %q; want %#x typed result", got, err, output.data, tc.want)
				}
			})
		}
	}
}

type spliceSlowWriter struct {
	started, release chan struct{}
	first            bool
	data             bytes.Buffer
}

func (w *spliceSlowWriter) Write(p []byte) (int, error) {
	if w.first {
		w.first = false
		close(w.started)
		<-w.release
	}
	return w.data.Write(p)
}

type observedSpliceOutput struct {
	p2.OutputStream
	entered chan struct{}
	once    sync.Once
}

func (out *observedSpliceOutput) WaitWritable(ctx context.Context) error {
	out.once.Do(func() { close(out.entered) })
	return out.OutputStream.WaitWritable(ctx)
}

func TestBlockingSpliceWaitsForAsyncWriter(t *testing.T) {
	writer := &spliceSlowWriter{started: make(chan struct{}), release: make(chan struct{}), first: true}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(writer.release) }) }
	defer release()
	adapter := p2.NewOutputStream(writer)
	output := &observedSpliceOutput{OutputStream: adapter, entered: make(chan struct{})}
	if err := adapter.TryWrite([]byte("pending")); err != nil {
		t.Fatal(err)
	}
	<-writer.started
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	controllerDone := make(chan struct{})
	go func() {
		defer close(controllerDone)
		select {
		case <-output.entered:
			release()
		case <-ctx.Done():
		}
	}()
	got, err := callSplice(t, p2.Config{Stdin: p2.NewInputStream(bytes.NewBufferString("abc")), Stdout: output}, ctx, 1, 3)
	cancel()
	<-controllerDone
	release()
	if waitErr := adapter.WaitWritable(context.Background()); waitErr != nil {
		t.Fatal(waitErr)
	}
	if err != nil || got != 3 || writer.data.String() != "pendingabc" {
		t.Fatalf("splice after asynchronous write = %d, %v, %q; want 3, nil, pendingabc", got, err, writer.data.String())
	}
}

type spliceRepeatedInput struct{}

func (spliceRepeatedInput) TryRead(dst []byte) (int, error) {
	dst[0] = 'x'
	return 1, nil
}
func (spliceRepeatedInput) WaitReadable(context.Context) error { return nil }

type spliceDiscardOutput struct{}

func (spliceDiscardOutput) CheckWrite() (uint64, error)        { return 4096, nil }
func (spliceDiscardOutput) TryWrite([]byte) error              { return nil }
func (spliceDiscardOutput) BeginFlush() error                  { return nil }
func (spliceDiscardOutput) WaitWritable(context.Context) error { return nil }

func BenchmarkBlockingSpliceReady(b *testing.B) {
	err := withSpliceInstance(b, context.Background(), p2.Config{Stdin: spliceRepeatedInput{}, Stdout: spliceDiscardOutput{}}, func(in *component.Instance) error {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			got, err := invokeSplice(in, context.Background(), 1, 1)
			if err != nil || got != 1 {
				return fmt.Errorf("ready splice = %d, %v", got, err)
			}
		}
		b.StopTimer()
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
}
