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

type pendingInputRead struct {
	ready  chan struct{}
	result readResult
}

// asyncInput adapts a synchronous reader without consuming it before component
// execution. At most one bounded read is in flight.
type asyncInput struct {
	r       io.Reader
	mu      sync.Mutex
	result  *readResult
	waiting *pendingInputRead
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
	read := &pendingInputRead{ready: make(chan struct{})}
	s.waiting = read
	go func() {
		buf := make([]byte, 64<<10)
		n, err := s.r.Read(buf)
		if n > 0 {
			buf = buf[:n]
		} else {
			buf = nil
		}
		read.result = readResult{b: buf, err: err}
		// Publishing the result before closing the channel wakes every waiter.
		// The close also orders their reads of the completed result.
		close(read.ready)
	}()
}

func (s *asyncInput) collect() {
	if s.waiting == nil {
		return
	}
	select {
	case <-s.waiting.ready:
		s.result = &s.waiting.result
		s.waiting = nil
	default:
	}
}

// pendingReadError reports readiness without consuming an error that must be
// delivered by the next read.
func (s *asyncInput) pendingReadError() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.collect()
	return s.result != nil && len(s.result.b) == 0 && s.result.err != nil
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
	if errors.Is(err, io.EOF) {
		s.closed = true
	} else if n > 0 && err != nil {
		// Deliver the bytes first, retaining the error for the next read.
		return n, nil
	}
	s.result = nil
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
	read := s.waiting
	s.mu.Unlock()
	select {
	case <-read.ready:
		s.mu.Lock()
		if s.waiting == read {
			s.waiting = nil
			s.result = &read.result
		}
		s.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type outputAdapter struct {
	w            io.Writer
	slots        chan struct{}
	mu           sync.Mutex
	last         <-chan struct{}
	err          error
	flushPending bool
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
		err = s.flush()
	} else {
		n, e := s.w.Write(request.data)
		err = e
		if err == nil && n != len(request.data) {
			err = io.ErrShortWrite
		}
	}
	for {
		s.mu.Lock()
		if err != nil && s.err == nil {
			s.err = err
		}
		if s.flushPending && !request.flush && s.err == nil {
			s.flushPending = false
			s.mu.Unlock()
			err = s.flush()
			request.flush = true
			continue
		}
		// A concurrent flush request is covered by the flush already in flight.
		s.flushPending = false
		<-s.slots
		close(request.done)
		s.mu.Unlock()
		return
	}
}

func (s *outputAdapter) flush() error {
	if f, ok := s.w.(interface{ Flush() error }); ok {
		return f.Flush()
	}
	return nil
}
func (s *outputAdapter) CheckWrite() (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return 0, s.err
	}
	if len(s.slots) != 0 {
		return 0, nil
	}
	return maxIOSize, nil
}
func (s *outputAdapter) TryWrite(p []byte) error {
	s.mu.Lock()
	if s.err != nil {
		err := s.err
		s.mu.Unlock()
		return err
	}
	// Empty writes consume no capacity and must not start underlying I/O.
	// Preserve any failure or closure observed since the caller's permit.
	if len(p) == 0 {
		s.mu.Unlock()
		return nil
	}
	select {
	case s.slots <- struct{}{}:
	default:
		s.mu.Unlock()
		return ErrWouldBlock
	}
	done := make(chan struct{})
	s.last = done
	s.mu.Unlock()
	go s.run(outputRequest{data: append([]byte(nil), p...), done: done})
	return nil
}
func (s *outputAdapter) BeginFlush() error {
	s.mu.Lock()
	if s.err != nil {
		err := s.err
		s.mu.Unlock()
		return err
	}
	if len(s.slots) != 0 {
		s.flushPending = true
		s.mu.Unlock()
		return nil
	}
	s.slots <- struct{}{}
	done := make(chan struct{})
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

func (s *hostState) addError(err error) (component.Value, error) {
	s.mu.Lock()
	if uint32(len(s.errors)) >= s.limits.MaxErrors {
		s.mu.Unlock()
		return nil, fmt.Errorf("wasi p2: stream error quota exceeded")
	}
	rep := s.nextError
	s.nextError++
	s.errors[rep] = streamErrorValue{err: err}
	t := s.resources
	s.mu.Unlock()
	return component.VariantValue{Disc: 0, Payload: t.NewOwn(errorResource, rep)}, nil
}

func (s *hostState) streamFailure(err error) ([]component.Value, error) {
	if errors.Is(err, io.EOF) {
		return []component.Value{component.ResultValue{IsErr: true, Payload: component.VariantValue{Disc: 1}}}, nil
	}
	v, quotaErr := s.addError(err)
	if quotaErr != nil {
		return nil, quotaErr
	}
	return []component.Value{component.ResultValue{IsErr: true, Payload: v}}, nil
}

// probeWritePermit records the latest CheckWrite result, including probes
// performed by blocking wrappers. An error invalidates an earlier grant.
func (s *hostState) probeWritePermit(rep uint32, w OutputStream) (uint64, error) {
	permit, err := w.CheckWrite()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		delete(s.permits, rep)
		return 0, err
	}
	if limit := s.limits.ioLimit(); permit > limit {
		permit = limit
	}
	s.permits[rep] = permit
	return permit, nil
}

// waitWritable distinguishes interruption of the caller from an asynchronous
// stream failure. Writers may themselves fail with a context error while the
// caller is still active; those failures belong in the stream-error result.
func (s *hostState) waitWritable(ctx context.Context, w OutputStream) ([]component.Value, error) {
	if err := w.WaitWritable(ctx); err != nil {
		if canceled := ctx.Err(); canceled != nil {
			return nil, canceled
		}
		return s.streamFailure(err)
	}
	return nil, nil
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

// consumeWritePermit applies write's one-shot permit to scalar requests before
// allocating their contents. Rejected writes consume the prior grant too.
func (s *hostState) consumeWritePermit(rep uint32, length uint64) error {
	s.mu.Lock()
	permit, granted := s.permits[rep]
	if granted {
		delete(s.permits, rep)
	}
	s.mu.Unlock()
	if !granted || length > permit || length > s.limits.ioLimit() {
		return fmt.Errorf("output-stream.write exceeds check-write permit")
	}
	return nil
}
