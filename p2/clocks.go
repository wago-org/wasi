package p2

import (
	"context"
	"fmt"
	"time"

	component "github.com/wago-org/component-model"
)

func clockOptions(s *hostState) []component.Option {
	mono := func(context.Context, []component.Value) ([]component.Value, error) {
		return []component.Value{uint64(time.Since(s.base))}, nil
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
	subscribe := func(instant bool) component.HostFunc {
		return func(_ context.Context, args []component.Value) ([]component.Value, error) {
			if len(args) != 1 {
				return nil, fmt.Errorf("monotonic-clock.subscribe: expected when")
			}
			n, ok := args[0].(uint64)
			if !ok {
				return nil, fmt.Errorf("monotonic-clock.subscribe: when is %T", args[0])
			}
			deadline := time.Now().Add(time.Duration(n))
			if instant {
				deadline = s.base.Add(time.Duration(n))
			}
			s.mu.Lock()
			rep := s.nextTimer
			s.nextTimer++
			s.deadlines[rep] = deadline
			s.mu.Unlock()
			return []component.Value{rep}, nil
		}
	}
	block := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		rep, err := repArg(args)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		deadline, ok := s.deadlines[rep]
		s.mu.Unlock()
		if ok {
			sleepUntil(ctx, deadline)
		}
		return nil, nil
	}
	poll := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("wasi:io/poll.poll: expected list")
		}
		list, ok := args[0].([]component.Value)
		if !ok {
			return nil, fmt.Errorf("wasi:io/poll.poll: got %T", args[0])
		}
		reps := make([]uint32, len(list))
		for i, x := range list {
			h, ok := x.(uint32)
			if !ok {
				return nil, fmt.Errorf("wasi:io/poll.poll: handle %d is %T", i, x)
			}
			rep, err := s.resources.Rep(pollableResource, h)
			if err != nil {
				return nil, err
			}
			reps[i] = rep
		}
		ready, earliest := s.ready(reps)
		if len(ready) == 0 && !earliest.IsZero() {
			sleepUntil(ctx, earliest)
			ready, _ = s.ready(reps)
		}
		return []component.Value{ready}, nil
	}
	readySub := func(resource uint32) component.Option {
		return custom(ifaceStreams, funcName(resource), func(context.Context, []component.Value) ([]component.Value, error) {
			return []component.Value{readyRep}, nil
		}, func(t *component.TypeTable) component.FuncDesc {
			return t.Func([]component.TypeRef{t.Borrow(resource)}, t.Own(pollableResource))
		})
	}
	return []component.Option{
		custom(ifaceMonoClock, "now", mono, u64Result),
		custom(ifaceMonoClock, "resolution", resolution, u64Result),
		custom(ifaceMonoClock, "subscribe-duration", subscribe(false), timerDesc),
		custom(ifaceMonoClock, "subscribe-instant", subscribe(true), timerDesc),
		custom(ifaceWallClock, "now", wall, datetimeResult),
		custom(ifaceWallClock, "resolution", wallRes, datetimeResult),
		custom(ifacePoll, "[method]pollable.block", block, func(t *component.TypeTable) component.FuncDesc {
			return t.Func([]component.TypeRef{t.Borrow(pollableResource)}, component.TypeRef{})
		}),
		custom(ifacePoll, "poll", poll, pollDesc),
		readySub(inputStreamResource),
		readySub(outputStreamResource),
	}
}

func funcName(resource uint32) string {
	if resource == inputStreamResource {
		return "[method]input-stream.subscribe"
	}
	return "[method]output-stream.subscribe"
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
		return 0, fmt.Errorf("pollable.block: expected self")
	}
	rep, ok := args[0].(uint32)
	if !ok {
		return 0, fmt.Errorf("pollable.block: self is %T", args[0])
	}
	return rep, nil
}
func sleepUntil(ctx context.Context, deadline time.Time) {
	d := time.Until(deadline)
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

func (s *hostState) ready(reps []uint32) ([]component.Value, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	out := make([]component.Value, 0, len(reps))
	var earliest time.Time
	for i, rep := range reps {
		deadline, timer := s.deadlines[rep]
		if !timer || !now.Before(deadline) {
			out = append(out, uint32(i))
		} else if earliest.IsZero() || deadline.Before(earliest) {
			earliest = deadline
		}
	}
	return out, earliest
}
