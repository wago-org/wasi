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

func TestInputReadyDoesNotAccumulatePrefixes(t *testing.T) {
	s := &hostState{stdin: &prefixedInput{prefix: []byte{'x'}, next: newInput(nil)}}
	for i := 0; i < 64; i++ {
		if !inputReady(s) {
			t.Fatalf("readiness check %d reported no buffered byte", i)
		}
	}
	depth := 0
	for in := s.stdin; in != nil; {
		prefix, ok := in.(*prefixedInput)
		if !ok {
			break
		}
		depth++
		in = prefix.next
	}
	if depth != 1 {
		t.Fatalf("64 readiness checks grew %d prefix wrappers, want 1", depth)
	}
	buf := make([]byte, 1)
	if n, err := s.stdin.TryRead(buf); n != 1 || err != nil || buf[0] != 'x' {
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
