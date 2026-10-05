package p2

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

type blockingReader struct{ release <-chan struct{} }

func (r blockingReader) Read([]byte) (int, error) { <-r.release; return 0, errors.New("released") }

func TestOptionsDoesNotPreconsumeStdin(t *testing.T) {
	release := make(chan struct{})
	done := make(chan struct{})
	go func() { Options(Config{Stdin: NewInputStream(blockingReader{release: release})}); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Options blocked consuming stdin")
	}
	close(release)
}

func TestAsyncInputWaitHonorsCancellation(t *testing.T) {
	release := make(chan struct{})
	in := newInput(blockingReader{release: release})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := in.WaitReadable(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitReadable error = %v", err)
	}
	close(release)
}

func TestInputReadyPreservesBufferedByteAcrossRepeatedChecks(t *testing.T) {
	in := &readinessErrorInput{withByte: true}
	s := &hostState{stdin: in}
	for i := 0; i < 64; i++ {
		if !inputReady(s) {
			t.Fatalf("readiness check %d reported no buffered byte", i)
		}
	}
	if in.calls != 1 {
		t.Fatalf("64 readiness checks made %d host reads, want 1", in.calls)
	}
	buf := make([]byte, 1)
	if n, err := s.readStdin(buf); n != 1 || err != nil || buf[0] != 'x' {
		t.Fatalf("buffered read = %q, %d, %v", buf, n, err)
	}
}

func TestPollableQuota(t *testing.T) {
	s := &hostState{pollables: map[uint32]pollableValue{}, nextPollable: 1, limits: Limits{MaxPollables: 1}.normalized()}
	if _, err := s.addPollable(pollableValue{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.addPollable(pollableValue{}); err == nil {
		t.Fatal("second pollable exceeded quota")
	}
}

func TestPollReportsOutputErrorAsReadiness(t *testing.T) {
	failed := false
	ready, err := waitPollables(context.Background(), []pollableValue{{
		ready: func() bool { return failed },
		wait: func(context.Context) error {
			failed = true
			return io.ErrClosedPipe
		},
	}})
	if err != nil || len(ready) != 1 || ready[0] != uint32(0) {
		t.Fatalf("poll after output error = %v, %v; want ready index 0", ready, err)
	}
}

func TestPollPropagatesWaitErrorWithoutReadiness(t *testing.T) {
	_, err := waitPollables(context.Background(), []pollableValue{{
		ready: func() bool { return false },
		wait:  func(context.Context) error { return context.Canceled },
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("poll wait error = %v, want context cancellation", err)
	}
}

func TestBlockReportsOutputErrorAsReadiness(t *testing.T) {
	failed := false
	err := blockPollable(context.Background(), pollableValue{
		ready: func() bool { return failed },
		wait: func(context.Context) error {
			failed = true
			return io.ErrClosedPipe
		},
	})
	if err != nil {
		t.Fatalf("block after output error = %v, want readiness without a host error", err)
	}
}

func TestBlockPropagatesWaitErrorWithoutReadiness(t *testing.T) {
	err := blockPollable(context.Background(), pollableValue{
		ready: func() bool { return false },
		wait:  func(context.Context) error { return context.Canceled },
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("block wait error = %v, want context cancellation", err)
	}
}

func TestStreamAndErrorQuotas(t *testing.T) {
	limits := Limits{MaxStreams: 2, MaxErrors: 1}.normalized()
	fs := newFilesystem(nil, limits)
	if err := fs.reserveStandardStream(); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.addStream(&fileStream{}); err != nil {
		t.Fatal(err)
	}
	if err := fs.reserveStandardStream(); err == nil {
		t.Fatal("standard stream exceeded the shared stream quota")
	}
	if _, err := fs.addStream(&fileStream{}); err == nil {
		t.Fatal("file stream exceeded the shared stream quota")
	}
	fs.releaseStandardStream()
	if err := fs.reserveStandardStream(); err != nil {
		t.Fatalf("dropped standard stream did not free its slot: %v", err)
	}

	s := &hostState{errors: map[uint32]streamErrorValue{1: {err: io.ErrUnexpectedEOF}}, limits: limits}
	if _, err := s.addError(io.ErrClosedPipe); err == nil {
		t.Fatal("stream error exceeded its quota")
	}
}

type blockingWriter struct{ release <-chan struct{} }

func (w blockingWriter) Write(p []byte) (int, error) {
	<-w.release
	return len(p), nil
}

func TestOutputAdapterDoesNotBlockGuestOnSlowWriter(t *testing.T) {
	release := make(chan struct{})
	out := newOutput(blockingWriter{release: release})
	returned := make(chan error, 1)
	go func() { returned <- out.TryWrite([]byte("payload")) }()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("nonblocking write waited for the host writer")
	}
	if n, err := out.CheckWrite(); err != nil || n != 0 {
		t.Fatalf("check-write while blocked = %d, %v", n, err)
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := out.WaitWritable(ctx); err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

type flushAfterWriteRecorder struct {
	started chan struct{}
	release <-chan struct{}
	mu      sync.Mutex
	events  []string
}

func (w *flushAfterWriteRecorder) Write(p []byte) (int, error) {
	close(w.started)
	<-w.release
	w.mu.Lock()
	w.events = append(w.events, "write")
	w.mu.Unlock()
	return len(p), nil
}

func (w *flushAfterWriteRecorder) Flush() error {
	w.mu.Lock()
	w.events = append(w.events, "flush")
	w.mu.Unlock()
	return nil
}

func TestOutputAdapterQueuesFlushAfterInFlightWrite(t *testing.T) {
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	w := &flushAfterWriteRecorder{started: make(chan struct{}), release: release}
	out := newOutput(w)
	if err := out.TryWrite([]byte("payload")); err != nil {
		t.Fatal(err)
	}
	<-w.started
	if err := out.BeginFlush(); err != nil {
		t.Fatalf("flush while a prior write is in flight: %v", err)
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := out.WaitWritable(ctx); err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if got := w.events; len(got) != 2 || got[0] != "write" || got[1] != "flush" {
		t.Fatalf("operation order = %v, want [write flush]", got)
	}
}

func TestWritePermitConsumption(t *testing.T) {
	for _, tc := range []struct {
		name           string
		permit, length uint64
		wantError      bool
	}{
		{"success", 32, 32, false}, {"zero-length", 32, 0, false},
		{"over-permit", 16, 17, true}, {"over-limit", 64, 33, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &hostState{permits: map[uint32]uint64{stdoutRep: tc.permit}, limits: Limits{MaxAggregateBufferBytes: 32}.normalized()}
			err := s.consumeWritePermit(stdoutRep, tc.length)
			if (err != nil) != tc.wantError {
				t.Fatalf("write permit error=%v, wantError=%v", err, tc.wantError)
			}
			if err := s.consumeWritePermit(stdoutRep, 0); err == nil {
				t.Fatal("write left a reusable permit")
			}
		})
	}
	s := &hostState{permits: map[uint32]uint64{}, limits: Limits{MaxAggregateBufferBytes: 32}.normalized()}
	allocs := testing.AllocsPerRun(8, func() {
		s.permits[stdoutRep] = 32
		if err := s.consumeWritePermit(stdoutRep, 32); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("successful permit consumption allocates %.0f times, want0", allocs)
	}
}
