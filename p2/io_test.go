package p2

import (
	"context"
	"errors"
	"io"
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
