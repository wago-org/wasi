package p2

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/wago-org/component-model"
)

// wasiPoll is the shared wasi:io/poll + wasi:clocks host. It owns the single
// timer-aware block/poll implementation -- replacing the former per-interface
// no-op copies in wasi_sockets.go (TCP+UDP) and wasi_http.go -- plus the
// wasi:clocks monotonic-clock and wall-clock funcs.
//
// # Pollable model
//
// Socket input and UDP receive subscriptions mint distinct pollables backed by
// readiness probes. Other immediately-ready streams may share
// wasiPollableRep. Clock subscriptions mint distinct reps with deadlines.
// poll checks every source and waits in short, cancellation-aware intervals
// until at least one callback or timer is ready.
//
// # Monotonic vs wall time
//
// base is captured at instance start; monotonic-clock.now returns nanoseconds
// since base (a real, monotonic reading via time.Since). subscribe-duration's
// deadline is now+when; subscribe-instant's is base+when (the absolute
// monotonic instant). wall-clock.now reads wallClock (WASIConfig.WallClock,
// defaulted to time.Now) -- the one injectable surface, so a test can pin the
// wall time for a deterministic assertion while real monotonic sleeps still
// elapse for real.
type wasiPoll struct {
	mu        sync.Mutex
	resources *component.HandleTable
	base      time.Time
	wallClock func() time.Time
	deadlines map[uint32]time.Time
	readiness map[uint32]func() bool
	nextRep   uint32
	timezone  *time.Location
}

func (p *wasiPoll) dropResource(rep uint32) {
	p.mu.Lock()
	delete(p.deadlines, rep)
	delete(p.readiness, rep)
	p.mu.Unlock()
}

// wasiPollTimerRepBase is wasiPoll.nextRep's start: any value but the
// always-ready singleton wasiPollableRep (1) works, since timer reps only ever
// need to be distinct from that one and from each other (all pollables share
// wasiPollableResType, and every non-timer pollable is rep 1).
const wasiPollTimerRepBase uint32 = 0x1000

// wasiIfaceClocksMonotonic / wasiIfaceClocksWall are the wasi:clocks interface
// names (version-stripped by mkImportKey, so the @x.y.z suffix is tolerant --
// see mkImportKey's doc).
const (
	wasiIfaceClocksMonotonic = "wasi:clocks/monotonic-clock@0.2.3"
	wasiIfaceClocksWall      = "wasi:clocks/wall-clock@0.2.3"
	wasiIfaceClocksTimezone  = "wasi:clocks/timezone@0.2.3"
)

// newWasiPoll builds the shared poll/clocks host. wallClock is never nil by the
// time WithWASI calls this (defaulted to time.Now there).
func newWasiPoll(wallClock func() time.Time) *wasiPoll {
	return &wasiPoll{
		base:      time.Now(),
		wallClock: wallClock,
		deadlines: make(map[uint32]time.Time),
		readiness: make(map[uint32]func() bool),
		nextRep:   wasiPollTimerRepBase,
	}
}

// setResources implements withResourcesHook's callback -- mirrors
// wasiFS.setResources's doc.
func (p *wasiPoll) setResources(t *component.HandleTable) {
	p.mu.Lock()
	p.resources = t
	p.mu.Unlock()
}

func (p *wasiPoll) getResources() (*component.HandleTable, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.resources == nil {
		return nil, fmt.Errorf("wasi:io/poll: resources handle table not yet initialized (setResources not called)")
	}
	return p.resources, nil
}

// newTimer mints a fresh timer pollable rep with the given absolute deadline.
func (p *wasiPoll) newTimer(deadline time.Time) uint32 {
	p.mu.Lock()
	defer p.mu.Unlock()
	rep := p.nextRep
	p.nextRep++
	p.deadlines[rep] = deadline
	return rep
}

func (p *wasiPoll) newReadiness(ready func() bool) uint32 {
	p.mu.Lock()
	defer p.mu.Unlock()
	rep := p.nextRep
	p.nextRep++
	p.readiness[rep] = ready
	return rep
}

func (p *wasiPoll) isReady(rep uint32, now time.Time) bool {
	p.mu.Lock()
	deadline, timer := p.deadlines[rep]
	readyFn := p.readiness[rep]
	p.mu.Unlock()
	if timer {
		return !now.Before(deadline)
	}
	if readyFn != nil {
		return readyFn()
	}
	return true
}

// deadlineOf returns rep's timer deadline, or ok=false if rep is not a timer
// pollable (i.e. an always-ready socket/stream/http pollable).
func (p *wasiPoll) deadlineOf(rep uint32) (time.Time, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	d, ok := p.deadlines[rep]
	return d, ok
}

// monotonicNow implements wasi:clocks/monotonic-clock.now() -> instant (u64 ns
// since base).
func (p *wasiPoll) monotonicNow(context.Context, []component.Value) ([]component.Value, error) {
	return []component.Value{uint64(time.Since(p.base))}, nil
}

// monotonicResolution implements monotonic-clock.resolution() -> duration.
// Go's time.Since resolves to the nanosecond, so 1ns is reported.
func (p *wasiPoll) monotonicResolution(context.Context, []component.Value) ([]component.Value, error) {
	return []component.Value{uint64(1)}, nil
}

// subscribeDuration implements monotonic-clock.subscribe-duration(when:
// duration) -> pollable: a timer that fires `when` nanoseconds from now. The
// bare rep is auto-wrapped into an own<pollable> handle (top-level own result
// -- see host_import.go's allocHandleResult).
func (p *wasiPoll) subscribeDuration(_ context.Context, args []component.Value) ([]component.Value, error) {
	when, err := wasiPollU64Arg("subscribe-duration", args)
	if err != nil {
		return nil, err
	}
	return []component.Value{p.newTimer(addUnsignedNanos(time.Now(), when))}, nil
}

// subscribeInstant implements monotonic-clock.subscribe-instant(when: instant)
// -> pollable: a timer that fires at the absolute monotonic instant `when`
// (base + when).
func (p *wasiPoll) subscribeInstant(_ context.Context, args []component.Value) ([]component.Value, error) {
	when, err := wasiPollU64Arg("subscribe-instant", args)
	if err != nil {
		return nil, err
	}
	return []component.Value{p.newTimer(addUnsignedNanos(p.base, when))}, nil
}

func addUnsignedNanos(base time.Time, nanos uint64) time.Time {
	if nanos > math.MaxInt64 {
		nanos = math.MaxInt64
	}
	return base.Add(time.Duration(nanos))
}

// wallNow implements wasi:clocks/wall-clock.now() -> datetime { seconds: u64,
// nanoseconds: u32 } from wallClock.
func (p *wasiPoll) wallNow(context.Context, []component.Value) ([]component.Value, error) {
	t := p.wallClock().UTC()
	return []component.Value{wasiDatetimeValue(t)}, nil
}

// wallResolution implements wall-clock.resolution() -> datetime. Reported as
// 1ns, matching monotonicResolution.
func (p *wasiPoll) wallResolution(context.Context, []component.Value) ([]component.Value, error) {
	return []component.Value{[]component.Value{uint64(0), uint32(1)}}, nil
}

func (p *wasiPoll) timezoneAt(args []component.Value) (time.Time, error) {
	if len(args) != 1 {
		return time.Time{}, fmt.Errorf("wasi:clocks/timezone: expected one datetime, got %d arguments", len(args))
	}
	fields, ok := args[0].([]component.Value)
	if !ok || len(fields) != 2 {
		return time.Time{}, fmt.Errorf("wasi:clocks/timezone: datetime: expected record, got %T", args[0])
	}
	seconds, sok := fields[0].(uint64)
	nanos, nok := fields[1].(uint32)
	if !sok || !nok || nanos >= 1_000_000_000 || seconds > uint64(math.MaxInt64) {
		return time.Time{}, fmt.Errorf("wasi:clocks/timezone: datetime is out of range")
	}
	loc := p.timezone
	if loc == nil {
		loc = time.UTC
	}
	return time.Unix(int64(seconds), int64(nanos)).In(loc), nil
}

func (p *wasiPoll) timezoneDisplay(_ context.Context, args []component.Value) ([]component.Value, error) {
	t, err := p.timezoneAt(args)
	if err != nil {
		return nil, err
	}
	name, offset := t.Zone()
	if offset <= -86400 || offset >= 86400 {
		name, offset, t = "UTC", 0, t.In(time.UTC)
	}
	return []component.Value{[]component.Value{int32(offset), name, t.IsDST()}}, nil
}

func (p *wasiPoll) timezoneUTCOffset(_ context.Context, args []component.Value) ([]component.Value, error) {
	t, err := p.timezoneAt(args)
	if err != nil {
		return nil, err
	}
	_, offset := t.Zone()
	if offset <= -86400 || offset >= 86400 {
		offset = 0
	}
	return []component.Value{int32(offset)}, nil
}

// block implements wasi:io/poll [method]pollable.block(self: borrow<pollable>)
// -> (): it waits for a timer or readiness callback; an always-ready pollable
// returns immediately. self is a top-level borrow, already resolved to a rep by
// liftHostArgs.
func (p *wasiPoll) block(ctx context.Context, args []component.Value) ([]component.Value, error) {
	rep, err := wasiPollU32Arg("[method]pollable.block", args)
	if err != nil {
		return nil, err
	}
	if err := p.waitForAny(ctx, []uint32{rep}); err != nil {
		return nil, err
	}
	return nil, nil
}

func (p *wasiPoll) ready(_ context.Context, args []component.Value) ([]component.Value, error) {
	rep, err := wasiPollU32Arg("[method]pollable.ready", args)
	if err != nil {
		return nil, err
	}
	return []component.Value{p.isReady(rep, time.Now())}, nil
}

// poll implements the free wasi:io/poll.poll(in: list<borrow<pollable>>) ->
// list<u32>. It resolves each handle to a live pollable rep (trapping loud on a
// bogus handle, matching every other borrow resolution). Always-ready pollables
// and already-due timers are reported ready immediately; if none are ready it
// blocks until the earliest timer deadline, then reports whatever is due --
// exactly poll's contract (block until >=1 ready, return all currently ready).
func (p *wasiPoll) poll(ctx context.Context, args []component.Value) ([]component.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("wasi:io/poll.poll: expected 1 arg (in), got %d", len(args))
	}
	list, ok := args[0].([]component.Value)
	if !ok {
		return nil, fmt.Errorf("wasi:io/poll.poll: in: expected list<borrow<pollable>> ([]component.Value), got %T", args[0])
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("wasi:io/poll.poll: input list must not be empty")
	}
	if uint64(len(list)) > math.MaxUint32 {
		return nil, fmt.Errorf("wasi:io/poll.poll: input list exceeds u32 index space")
	}
	resources, err := p.getResources()
	if err != nil {
		return nil, err
	}
	// Resolve every handle to a rep once, up front (also validates them).
	reps := make([]uint32, len(list))
	for i, v := range list {
		h, ok := v.(uint32)
		if !ok {
			return nil, fmt.Errorf("wasi:io/poll.poll: in[%d]: expected uint32 handle, got %T", i, v)
		}
		rep, err := resources.Rep(wasiPollableResType, h)
		if err != nil {
			return nil, fmt.Errorf("wasi:io/poll.poll: in[%d]: %w", i, err)
		}
		reps[i] = rep
	}
	if out := p.readyIndices(reps); len(out) > 0 {
		return []component.Value{out}, nil
	}
	if err := p.waitForAny(ctx, reps); err != nil {
		return nil, err
	}
	return []component.Value{p.readyIndices(reps)}, nil
}

func (p *wasiPoll) waitForAny(ctx context.Context, reps []uint32) error {
	for len(p.readyIndices(reps)) == 0 {
		wait := time.Millisecond
		if earliest, ok := p.earliestDeadline(reps); ok {
			if until := time.Until(earliest); until < wait {
				wait = until
			}
		}
		if wait <= 0 {
			return nil
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		}
	}
	return nil
}

// readyIndices returns the indices of reps whose timer, readiness callback, or
// always-ready singleton is ready now.
func (p *wasiPoll) readyIndices(reps []uint32) []component.Value {
	now := time.Now()
	out := make([]component.Value, 0, len(reps))
	for i, rep := range reps {
		if p.isReady(rep, now) {
			out = append(out, uint32(i))
		}
	}
	return out
}

// earliestDeadline returns the soonest timer deadline among reps.
func (p *wasiPoll) earliestDeadline(reps []uint32) (time.Time, bool) {
	var earliest time.Time
	found := false
	for _, rep := range reps {
		if d, ok := p.deadlineOf(rep); ok {
			if !found || d.Before(earliest) {
				earliest, found = d, true
			}
		}
	}
	return earliest, found
}

// wasiSleepUntil sleeps until deadline, waking early if ctx is cancelled (so a
// cancelled guest call does not hang on a long timer). A non-positive remaining
// duration returns immediately.
func wasiSleepUntil(ctx context.Context, deadline time.Time) {
	d := time.Until(deadline)
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// wasiDatetimeValue builds the wasi:clocks/wall-clock `datetime` record value
// (seconds: u64, nanoseconds: u32) from a time.Time.
func wasiDatetimeValue(t time.Time) component.Value {
	return []component.Value{uint64(t.Unix()), uint32(t.Nanosecond())}
}

// wasiPollU64Arg parses a single-u64-arg func's args.
func wasiPollU64Arg(method string, args []component.Value) (uint64, error) {
	if len(args) != 1 {
		return 0, fmt.Errorf("wasi:clocks/monotonic-clock.%s: expected 1 arg (when), got %d", method, len(args))
	}
	when, ok := args[0].(uint64)
	if !ok {
		return 0, fmt.Errorf("wasi:clocks/monotonic-clock.%s: when: expected uint64, got %T", method, args[0])
	}
	return when, nil
}

// wasiPollU32Arg parses a single-u32-rep-arg func's args (a resolved borrow
// self).
func wasiPollU32Arg(method string, args []component.Value) (uint32, error) {
	if len(args) != 1 {
		return 0, fmt.Errorf("%s: expected 1 arg (self), got %d", method, len(args))
	}
	rep, ok := args[0].(uint32)
	if !ok {
		return 0, fmt.Errorf("%s: self: expected uint32 rep, got %T", method, args[0])
	}
	return rep, nil
}

// wasiClockPollOptions returns the centralized wasi:io/poll (block, poll,
// pollable resource tag) + wasi:clocks (monotonic + wall-clock) Options,
// registered unconditionally by WithWASI so any guest -- sockets, http, clocks,
// or a bare stream-poller -- shares one timer-aware implementation.
func wasiClockPollOptions(p *wasiPoll) []component.Option {
	blockFD, blockR := wasiPollableBlockSig()
	readyFD, readyR := wasiPollableReadySig()
	pollFD, pollR := wasiPollSig()
	monoNowFD, monoNowR := wasiMonotonicNowSig()
	monoSubFD, monoSubR := wasiMonotonicSubscribeSig()
	monoResFD, monoResR := wasiMonotonicNowSig() // () -> u64, same shape as now
	wallNowFD, wallNowR := wasiWallClockNowSig()
	wallResFD, wallResR := wasiWallClockNowSig() // () -> datetime, same shape as now
	tzDisplayFD, tzDisplayR := wasiTimezoneDisplaySig()
	tzOffsetFD, tzOffsetR := wasiTimezoneOffsetSig()

	return []component.Option{
		component.WithResourcesHook(p.setResources),
		component.WithResourceTag(wasiIfacePoll, "pollable", wasiPollableResType),

		component.WithImportCustom(wasiIfacePoll, "[method]pollable.block", p.block, blockFD, blockR),
		component.WithImportCustom(wasiIfacePoll, "[method]pollable.ready", p.ready, readyFD, readyR),
		component.WithImportCustom(wasiIfacePoll, "poll", p.poll, pollFD, pollR),

		component.WithImportCustom(wasiIfaceClocksMonotonic, "now", p.monotonicNow, monoNowFD, monoNowR),
		component.WithImportCustom(wasiIfaceClocksMonotonic, "resolution", p.monotonicResolution, monoResFD, monoResR),
		component.WithImportCustom(wasiIfaceClocksMonotonic, "subscribe-duration", p.subscribeDuration, monoSubFD, monoSubR),
		component.WithImportCustom(wasiIfaceClocksMonotonic, "subscribe-instant", p.subscribeInstant, monoSubFD, monoSubR),

		component.WithImportCustom(wasiIfaceClocksWall, "now", p.wallNow, wallNowFD, wallNowR),
		component.WithImportCustom(wasiIfaceClocksWall, "resolution", p.wallResolution, wallResFD, wallResR),
		component.WithImportCustom(wasiIfaceClocksTimezone, "display", p.timezoneDisplay, tzDisplayFD, tzDisplayR),
		component.WithImportCustom(wasiIfaceClocksTimezone, "utc-offset", p.timezoneUTCOffset, tzOffsetFD, tzOffsetR),
	}
}

func wasiTimezoneDisplaySig() (component.FuncDesc, component.Resolver) {
	tbl := &typeTable{}
	datetime := wasiDatetimeType(tbl)
	display := tbl.add(component.RecordDesc{Fields: []component.RecordField{
		{Name: "utc-offset", Type: component.TypeRef{Primitive: "s32"}},
		{Name: "name", Type: component.TypeRef{Primitive: "string"}},
		{Name: "in-daylight-saving-time", Type: component.TypeRef{Primitive: "bool"}},
	}})
	return component.FuncDesc{Params: []component.FuncParam{{Name: "when", Type: datetime}}, Results: component.FuncResults{Unnamed: &display}}, tbl.resolver()
}

func wasiTimezoneOffsetSig() (component.FuncDesc, component.Resolver) {
	tbl := &typeTable{}
	datetime := wasiDatetimeType(tbl)
	offset := component.TypeRef{Primitive: "s32"}
	return component.FuncDesc{Params: []component.FuncParam{{Name: "when", Type: datetime}}, Results: component.FuncResults{Unnamed: &offset}}, tbl.resolver()
}

func wasiPollableReadySig() (component.FuncDesc, component.Resolver) {
	tbl := &typeTable{}
	selfRef := tbl.add(component.BorrowDesc{ResourceType: wasiPollableResType})
	result := component.TypeRef{Primitive: "bool"}
	return component.FuncDesc{
		Params:  []component.FuncParam{{Name: "self", Type: selfRef}},
		Results: component.FuncResults{Unnamed: &result},
	}, tbl.resolver()
}

// wasiMonotonicNowSig builds the FuncDesc/resolver for monotonic-clock.now() ->
// instant (u64) -- also reused for resolution() -> duration (u64), same shape.
func wasiMonotonicNowSig() (component.FuncDesc, component.Resolver) {
	tbl := &typeTable{}
	ref := component.TypeRef{Primitive: "u64"}
	fd := component.FuncDesc{Results: component.FuncResults{Unnamed: &ref}}
	return fd, tbl.resolver()
}

// wasiMonotonicSubscribeSig builds the FuncDesc/resolver for a subscribe-*(when:
// u64) -> own<pollable> method (subscribe-duration and subscribe-instant share
// it).
func wasiMonotonicSubscribeSig() (component.FuncDesc, component.Resolver) {
	tbl := &typeTable{}
	pollRef := tbl.add(component.OwnDesc{ResourceType: wasiPollableResType})
	fd := component.FuncDesc{
		Params:  []component.FuncParam{{Name: "when", Type: component.TypeRef{Primitive: "u64"}}},
		Results: component.FuncResults{Unnamed: &pollRef},
	}
	return fd, tbl.resolver()
}

// wasiWallClockNowSig builds the FuncDesc/resolver for wall-clock.now() ->
// datetime -- also reused for resolution() -> datetime, same shape.
func wasiWallClockNowSig() (component.FuncDesc, component.Resolver) {
	tbl := &typeTable{}
	dtRef := wasiDatetimeType(tbl)
	fd := component.FuncDesc{Results: component.FuncResults{Unnamed: &dtRef}}
	return fd, tbl.resolver()
}
