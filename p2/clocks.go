package p2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	component "github.com/wago-org/component-model"
)

func clockOptions(s *hostState, fs *filesystemState) []component.Option {
	monoNow := func() uint64 { return uint64(time.Since(s.base)) }
	mono := func(context.Context, []component.Value) ([]component.Value, error) {
		return []component.Value{monoNow()}, nil
	}
	resolution := func(context.Context, []component.Value) ([]component.Value, error) {
		return []component.Value{uint64(1)}, nil
	}
	wall := func(context.Context, []component.Value) ([]component.Value, error) {
		n := s.wall().UTC()
		return []component.Value{[]component.Value{uint64(n.Unix()), uint32(n.Nanosecond())}}, nil
	}
	wallRes := func(context.Context, []component.Value) ([]component.Value, error) {
		return []component.Value{[]component.Value{uint64(0), uint32(1)}}, nil
	}
	subscribeTimer := func(instant bool) component.HostFunc {
		return func(_ context.Context, args []component.Value) ([]component.Value, error) {
			if len(args) != 1 {
				return nil, fmt.Errorf("monotonic-clock.subscribe: expected when")
			}
			n, ok := args[0].(uint64)
			if !ok {
				return nil, fmt.Errorf("monotonic-clock.subscribe: when is %T", args[0])
			}
			target := n
			if !instant {
				now := monoNow()
				if math.MaxUint64-now < n {
					target = math.MaxUint64
				} else {
					target = now + n
				}
			}
			p := pollableValue{ready: func() bool { return monoNow() >= target }, wait: func(ctx context.Context) error { return waitMonotonic(ctx, monoNow, target) }}
			rep, err := s.addPollable(p)
			if err != nil {
				return nil, err
			}
			return []component.Value{rep}, nil
		}
	}
	lookup := func(rep uint32) (pollableValue, bool) {
		s.mu.Lock()
		defer s.mu.Unlock()
		p, ok := s.pollables[rep]
		return p, ok
	}
	block := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		rep, err := repArg(args)
		if err != nil {
			return nil, err
		}
		p, ok := lookup(rep)
		if !ok {
			return nil, fmt.Errorf("pollable.block: unknown rep %d", rep)
		}
		return nil, blockPollable(ctx, p)
	}
	readyMethod := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		rep, err := repArg(args)
		if err != nil {
			return nil, err
		}
		p, ok := lookup(rep)
		if !ok {
			return nil, fmt.Errorf("pollable.ready: unknown rep %d", rep)
		}
		return []component.Value{p.ready()}, nil
	}
	poll := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("wasi:io/poll.poll: expected list")
		}
		list, ok := args[0].([]component.Value)
		if !ok {
			return nil, fmt.Errorf("wasi:io/poll.poll: got %T", args[0])
		}
		if len(list) == 0 {
			return nil, fmt.Errorf("wasi:io/poll.poll: empty list")
		}
		if uint32(len(list)) > s.limits.MaxPollInputs {
			return nil, fmt.Errorf("wasi:io/poll.poll: input quota exceeded")
		}
		ps := make([]pollableValue, len(list))
		for i, x := range list {
			h, ok := x.(uint32)
			if !ok {
				return nil, fmt.Errorf("wasi:io/poll.poll: handle %d is %T", i, x)
			}
			rep, err := s.resources.Rep(pollableResource, h)
			if err != nil {
				return nil, err
			}
			p, exists := lookup(rep)
			if !exists {
				return nil, fmt.Errorf("wasi:io/poll.poll: unknown rep %d", rep)
			}
			ps[i] = p
		}
		ready, err := waitPollables(ctx, ps)
		if err != nil {
			return nil, err
		}
		return []component.Value{ready}, nil
	}
	subscribeInput := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		rep, err := repArg(args)
		if err != nil {
			return nil, err
		}
		var p pollableValue
		if rep == stdinRep {
			p = pollableValue{ready: func() bool { return inputReady(s) }, wait: s.waitStdin}
		} else if fs.input(rep) != nil {
			p = pollableValue{ready: func() bool { return true }, wait: func(context.Context) error { return nil }}
		} else {
			return nil, fmt.Errorf("input-stream.subscribe: unknown rep %d", rep)
		}
		newRep, err := s.addPollable(p)
		if err != nil {
			return nil, err
		}
		return []component.Value{newRep}, nil
	}
	subscribeOutput := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		rep, err := repArg(args)
		if err != nil {
			return nil, err
		}
		var out OutputStream
		switch rep {
		case stdoutRep:
			out = s.stdout
		case stderrRep:
			out = s.stderr
		default:
			s.mu.Lock()
			out = s.outputs[rep]
			s.mu.Unlock()
			if out == nil {
				if w := fs.output(rep); w != nil {
					candidate := newOutput(w)
					s.mu.Lock()
					if prior := s.outputs[rep]; prior != nil {
						out = prior
					} else {
						s.outputs[rep] = candidate
						out = candidate
					}
					s.mu.Unlock()
				}
			}
		}
		if out == nil {
			return nil, fmt.Errorf("output-stream.subscribe: unknown rep %d", rep)
		}
		p := pollableValue{
			ready: func() bool {
				if s.outputClosed(rep) {
					return true
				}
				n, err := out.CheckWrite()
				return err != nil || n > 0
			},
			wait: func(ctx context.Context) error {
				if s.outputClosed(rep) {
					return nil
				}
				return out.WaitWritable(ctx)
			},
		}
		newRep, err := s.addPollable(p)
		if err != nil {
			return nil, err
		}
		return []component.Value{newRep}, nil
	}
	return []component.Option{
		custom(ifaceMonoClock, "now", mono, u64Result), custom(ifaceMonoClock, "resolution", resolution, u64Result),
		custom(ifaceMonoClock, "subscribe-duration", subscribeTimer(false), timerDesc), custom(ifaceMonoClock, "subscribe-instant", subscribeTimer(true), timerDesc),
		custom(ifaceWallClock, "now", wall, datetimeResult), custom(ifaceWallClock, "resolution", wallRes, datetimeResult),
		custom(ifacePoll, "[method]pollable.ready", readyMethod, boolMethodDesc), custom(ifacePoll, "[method]pollable.block", block, emptyMethodDesc(pollableResource)), custom(ifacePoll, "poll", poll, pollDesc),
		custom(ifaceStreams, "[method]input-stream.subscribe", subscribeInput, subscribeDesc(inputStreamResource)), custom(ifaceStreams, "[method]output-stream.subscribe", subscribeOutput, subscribeDesc(outputStreamResource)),
		component.WithHostResourceDtor(pollableResource, func(_ context.Context, rep uint32) error {
			s.mu.Lock()
			delete(s.pollables, rep)
			s.mu.Unlock()
			return nil
		}),
	}
}

func blockPollable(ctx context.Context, p pollableValue) error {
	retryDelay := time.Millisecond
	for !p.ready() {
		err := p.wait(ctx)
		if canceled := ctx.Err(); canceled != nil {
			return canceled
		}
		if err != nil && !p.ready() {
			return err
		}
		if !p.ready() {
			if err := waitPollRetry(ctx, &retryDelay); err != nil {
				return err
			}
		}
	}
	return nil
}

func waitPollables(ctx context.Context, ps []pollableValue) ([]component.Value, error) {
	if ready := readyIndexes(ps); len(ready) != 0 {
		return ready, nil
	}
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type waitResult struct {
		err    error
		resume chan struct{}
	}
	ch := make(chan waitResult, len(ps))
	for _, p := range ps {
		go func(p pollableValue) {
			retryDelay := time.Millisecond
			for {
				result := waitResult{err: p.wait(waitCtx), resume: make(chan struct{})}
				select {
				case ch <- result:
				case <-waitCtx.Done():
					return
				}
				select {
				case <-result.resume:
				case <-waitCtx.Done():
					return
				}
				if err := waitPollRetry(waitCtx, &retryDelay); err != nil {
					return
				}
			}
		}(p)
	}
	for {
		var result waitResult
		select {
		case result = <-ch:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if canceled := ctx.Err(); canceled != nil {
			return nil, canceled
		}
		if ready := readyIndexes(ps); len(ready) != 0 {
			return ready, nil
		}
		if result.err != nil {
			return nil, result.err
		}
		// Readiness was consumed before the recheck. Resume only this worker;
		// each pollable has at most one waiter even after repeated wakeups.
		close(result.resume)
	}
}

func waitPollRetry(ctx context.Context, delay *time.Duration) error {
	timer := time.NewTimer(*delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return ctx.Err()
	}
	if *delay < 50*time.Millisecond {
		*delay *= 2
		if *delay > 50*time.Millisecond {
			*delay = 50 * time.Millisecond
		}
	}
	return nil
}

func (s *hostState) readStdin(dst []byte) (int, error) {
	s.stdinMu.Lock()
	defer s.stdinMu.Unlock()
	return s.readStdinLocked(dst)
}

// readStdinLocked requires stdinMu to be held by the caller.
func (s *hostState) readStdinLocked(dst []byte) (int, error) {
	if s.stdinBuffered {
		n := copy(dst, s.stdinProbe[:])
		if n != 0 {
			s.stdinBuffered = false
		}
		return n, nil
	}
	if s.stdinReadError != nil {
		err := s.stdinReadError
		if !errors.Is(err, io.EOF) {
			if len(dst) == 0 {
				return 0, nil // An open zero read must preserve a deferred error.
			}
			s.stdinReadError = io.EOF
		}
		return 0, err
	}
	n, err := s.stdin.TryRead(dst)
	if err != nil && !errors.Is(err, ErrWouldBlock) {
		if n > 0 && !errors.Is(err, io.EOF) {
			s.stdinReadError = err // Deliver bytes, then the failure, then closed.
		} else {
			s.stdinReadError = io.EOF
		}
	}
	return n, err
}

func (s *hostState) waitStdin(ctx context.Context) error {
	s.stdinMu.Lock()
	if s.stdinBuffered || s.stdinReadError != nil {
		s.stdinMu.Unlock()
		return nil
	}
	in := s.stdin
	s.stdinMu.Unlock()
	err := in.WaitReadable(ctx)
	if err != nil && ctx.Err() == nil {
		s.stdinMu.Lock()
		if s.stdinReadError == nil {
			s.stdinReadError = err
		}
		s.stdinMu.Unlock()
		return nil // A stream failure makes its pollable ready.
	}
	return err
}

func inputReady(s *hostState) bool {
	s.stdinMu.Lock()
	defer s.stdinMu.Unlock()
	if s.stdinBuffered || s.stdinReadError != nil {
		return true
	}
	if in, ok := s.stdin.(*asyncInput); ok && in.pendingReadError() {
		return true
	}
	// Keep the probe's byte and error until a guest read consumes them. Using
	// instance storage also avoids allocating a byte slice and prefix wrapper
	// on every successful probe.
	n, err := s.stdin.TryRead(s.stdinProbe[:])
	s.stdinBuffered = n > 0
	if err != nil && !errors.Is(err, ErrWouldBlock) {
		s.stdinReadError = err
	}
	return s.stdinBuffered || s.stdinReadError != nil
}
func readyIndexes(ps []pollableValue) []component.Value {
	out := make([]component.Value, 0, len(ps))
	for i, p := range ps {
		if p.ready() {
			out = append(out, uint32(i))
		}
	}
	return out
}
func waitMonotonic(ctx context.Context, now func() uint64, target uint64) error {
	for {
		n := now()
		if n >= target {
			return nil
		}
		d := target - n
		if d > uint64(math.MaxInt64) {
			d = uint64(math.MaxInt64)
		}
		timer := time.NewTimer(time.Duration(d))
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}
func u64Result(t *component.TypeTable) component.FuncDesc { return t.Func(nil, component.Prim("u64")) }
func timerDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{component.Prim("u64")}, t.Own(pollableResource))
}
func datetimeResult(t *component.TypeTable) component.FuncDesc {
	return t.Func(nil, t.Record("seconds", component.Prim("u64"), "nanoseconds", component.Prim("u32")))
}
func pollDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.List(t.Borrow(pollableResource))}, t.List(component.Prim("u32")))
}
func repArg(args []component.Value) (uint32, error) {
	if len(args) != 1 {
		return 0, fmt.Errorf("expected self")
	}
	rep, ok := args[0].(uint32)
	if !ok {
		return 0, fmt.Errorf("self is %T", args[0])
	}
	return rep, nil
}
func boolMethodDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(pollableResource)}, component.Prim("bool"))
}
func emptyMethodDesc(resource uint32) func(*component.TypeTable) component.FuncDesc {
	return func(t *component.TypeTable) component.FuncDesc {
		return t.Func([]component.TypeRef{t.Borrow(resource)}, component.TypeRef{})
	}
}
func subscribeDesc(resource uint32) func(*component.TypeTable) component.FuncDesc {
	return func(t *component.TypeTable) component.FuncDesc {
		return t.Func([]component.TypeRef{t.Borrow(resource)}, t.Own(pollableResource))
	}
}
