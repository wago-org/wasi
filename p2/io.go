package p2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	component "github.com/wago-org/component-model"
)

// InputStream is the nonblocking host-side contract used by WASI streams.
// TryRead must return ErrWouldBlock when no input is currently available.
type InputStream interface {
	TryRead([]byte) (int, error)
	WaitReadable(context.Context) error
}

// OutputStream is the nonblocking host-side contract used by WASI streams.
// CheckWrite grants the maximum number of bytes accepted by the next TryWrite.
type OutputStream interface {
	CheckWrite() (uint64, error)
	TryWrite([]byte) error
	BeginFlush() error
	WaitWritable(context.Context) error
}

// ErrWouldBlock reports that a nonblocking stream operation is not ready.
var ErrWouldBlock = errors.New("wasi p2: operation would block")

type readResult struct {
	b   []byte
	err error
}

// asyncInput adapts a synchronous reader without consuming it before component
// execution. At most one bounded read is in flight.
type asyncInput struct {
	r       io.Reader
	mu      sync.Mutex
	result  *readResult
	waiting chan readResult
	closed  bool
}

func newInput(r io.Reader) InputStream {
	if r == nil {
		return &asyncInput{closed: true}
	}
	if in, ok := r.(InputStream); ok {
		return in
	}
	return &asyncInput{r: r}
}

// NewInputStream adapts a synchronous reader to the nonblocking WASI stream
// contract. A nil reader produces an input stream that is already closed.
func NewInputStream(r io.Reader) InputStream { return newInput(r) }

func (s *asyncInput) start() {
	if s.waiting != nil || s.result != nil || s.closed {
		return
	}
	ch := make(chan readResult, 1)
	s.waiting = ch
	go func() {
		buf := make([]byte, 64<<10)
		n, err := s.r.Read(buf)
		if n > 0 {
			buf = buf[:n]
		} else {
			buf = nil
		}
		ch <- readResult{b: buf, err: err}
	}()
}

func (s *asyncInput) collect() {
	if s.waiting == nil {
		return
	}
	select {
	case result := <-s.waiting:
		s.waiting = nil
		s.result = &result
	default:
	}
}

func (s *asyncInput) TryRead(dst []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.collect()
	if s.result == nil {
		if s.closed {
			return 0, io.EOF
		}
		s.start()
		s.collect()
	}
	if s.result == nil {
		return 0, ErrWouldBlock
	}
	n := copy(dst, s.result.b)
	s.result.b = s.result.b[n:]
	if len(s.result.b) != 0 {
		return n, nil
	}
	err := s.result.err
	s.result = nil
	if errors.Is(err, io.EOF) {
		s.closed = true
	}
	if n > 0 {
		return n, nil
	}
	return 0, err
}

func (s *asyncInput) WaitReadable(ctx context.Context) error {
	s.mu.Lock()
	s.collect()
	if s.result != nil || s.closed {
		s.mu.Unlock()
		return nil
	}
	s.start()
	ch := s.waiting
	s.mu.Unlock()
	select {
	case result := <-ch:
		s.mu.Lock()
		if s.waiting == ch {
			s.waiting = nil
			s.result = &result
		}
		s.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type outputAdapter struct {
	w     io.Writer
	slots chan struct{}
	mu    sync.Mutex
	last  <-chan struct{}
	err   error
}

type outputRequest struct {
	data  []byte
	flush bool
	done  chan struct{}
}

func newOutput(w io.Writer) OutputStream {
	if w == nil {
		w = io.Discard
	}
	if out, ok := w.(OutputStream); ok {
		return out
	}
	return &outputAdapter{w: w, slots: make(chan struct{}, 1)}
}

// NewOutputStream adapts a synchronous writer to the nonblocking WASI stream
// contract. A nil writer discards output.
func NewOutputStream(w io.Writer) OutputStream { return newOutput(w) }

func (s *outputAdapter) run(request outputRequest) {
	var err error
	if request.flush {
		if f, ok := s.w.(interface{ Flush() error }); ok {
			err = f.Flush()
		}
	} else {
		n, e := s.w.Write(request.data)
		err = e
		if err == nil && n != len(request.data) {
			err = io.ErrShortWrite
		}
	}
	s.mu.Lock()
	if err != nil && s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
	<-s.slots
	close(request.done)
}
func (s *outputAdapter) CheckWrite() (uint64, error) {
	s.mu.Lock()
	err := s.err
	s.mu.Unlock()
	if err != nil {
		return 0, err
	}
	if len(s.slots) != 0 {
		return 0, nil
	}
	return maxIOSize, nil
}
func (s *outputAdapter) TryWrite(p []byte) error {
	select {
	case s.slots <- struct{}{}:
	default:
		return ErrWouldBlock
	}
	done := make(chan struct{})
	s.mu.Lock()
	s.last = done
	s.mu.Unlock()
	go s.run(outputRequest{data: append([]byte(nil), p...), done: done})
	return nil
}
func (s *outputAdapter) BeginFlush() error {
	select {
	case s.slots <- struct{}{}:
	default:
		return ErrWouldBlock
	}
	done := make(chan struct{})
	s.mu.Lock()
	s.last = done
	s.mu.Unlock()
	go s.run(outputRequest{flush: true, done: done})
	return nil
}
func (s *outputAdapter) WaitWritable(ctx context.Context) error {
	s.mu.Lock()
	last, err := s.last, s.err
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if last == nil {
		return nil
	}
	select {
	case <-last:
		s.mu.Lock()
		err = s.err
		s.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type streamErrorValue struct {
	err error
}

type pollableValue struct {
	ready func() bool
	wait  func(context.Context) error
}

func (s *hostState) addError(err error) component.Value {
	s.mu.Lock()
	rep := s.nextError
	s.nextError++
	s.errors[rep] = streamErrorValue{err: err}
	t := s.resources
	s.mu.Unlock()
	return component.VariantValue{Disc: 0, Payload: t.NewOwn(errorResource, rep)}
}

func (s *hostState) streamFailure(err error) []component.Value {
	if errors.Is(err, io.EOF) {
		return []component.Value{component.ResultValue{IsErr: true, Payload: component.VariantValue{Disc: 1}}}
	}
	return []component.Value{component.ResultValue{IsErr: true, Payload: s.addError(err)}}
}

func (s *hostState) addPollable(v pollableValue) (uint32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if uint32(len(s.pollables)) >= s.limits.MaxPollables {
		return 0, fmt.Errorf("pollable quota exceeded")
	}
	rep := s.nextPollable
	s.nextPollable++
	s.pollables[rep] = v
	return rep, nil
}
