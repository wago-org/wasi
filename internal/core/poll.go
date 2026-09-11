package core

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"time"

	wago "github.com/wago-org/wago"
)

// Pollable is the readiness contract for configured streams that are not
// backed by an *os.File. Wait must return when ctx is canceled.
type Pollable interface {
	Ready() bool
	Wait(context.Context) error
}

type pollSubscription struct {
	userdata uint64
	typ      byte
	entry    *fdEntry
	due      time.Duration
	clock    bool
	code     uint16
}

// pollOneoff implements Preview 1 polling without claiming readiness for an
// arbitrary synchronous reader or writer. Regular files and EOF/discard streams
// are immediately ready; OS descriptors use poll(2), and custom streams must
// implement Pollable.
func (e *Plugin) pollOneoff(m wago.HostModule, p, r []uint64) {
	in, out, n, result := uint32(p[0]), uint32(p[1]), uint32(p[2]), uint32(p[3])
	mem := m.Memory()
	if n == 0 {
		r[0] = wasiEInval
		return
	}
	limit := e.cfg.MaxSubscriptionsPerPoll
	if limit == 0 {
		limit = 1024
	}
	if n > limit {
		r[0] = wasiENomem
		return
	}
	if uint64(in)+uint64(n)*48 > uint64(len(mem)) || uint64(out)+uint64(n)*32 > uint64(len(mem)) {
		r[0] = wasiEFault
		return
	}

	subs := make([]pollSubscription, 0, n)
	var earliest time.Duration
	hasDeadline := false
	for i := uint32(0); i < n; i++ {
		raw := mem[in+i*48 : in+(i+1)*48]
		sub := pollSubscription{userdata: binary.LittleEndian.Uint64(raw), typ: raw[8]}
		switch sub.typ {
		case 0:
			clockID := binary.LittleEndian.Uint32(raw[16:])
			timeout := binary.LittleEndian.Uint64(raw[24:])
			flags := binary.LittleEndian.Uint16(raw[40:])
			if clockID > 3 || flags&^uint16(1) != 0 {
				r[0] = wasiEInval
				return
			}
			now, _, err := clockValue(e.cfg.Clocks, clockID)
			if err != nil {
				r[0] = wasiENotsup
				return
			}
			remaining := timeout
			if flags&1 != 0 {
				if timeout > now {
					remaining = timeout - now
				} else {
					remaining = 0
				}
			}
			if remaining > uint64(^uint64(0)>>1) {
				r[0] = wasiEOverflow
				return
			}
			sub.clock = true
			sub.due = time.Duration(remaining)
			if !hasDeadline || sub.due < earliest {
				earliest, hasDeadline = sub.due, true
			}
		case 1, 2:
			fd := binary.LittleEndian.Uint32(raw[16:])
			entry, code := e.entry(fd)
			if code == wasiOK {
				code = require(entry, rightPollFDReadWrite)
			}
			sub.entry = entry
			sub.code = uint16(code)
		default:
			r[0] = wasiEInval
			return
		}
		subs = append(subs, sub)
	}

	started := time.Now()
	ready := readySubscriptions(subs, 0)
	if len(ready) == 0 {
		if err := e.waitSubscriptions(subs, earliest, hasDeadline); err != nil {
			switch {
			case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
				r[0] = wasiEIntr
			case errors.Is(err, errPollUnsupported):
				r[0] = wasiENotsup
			default:
				r[0] = errno(err)
			}
			return
		}
		ready = readySubscriptions(subs, time.Since(started))
	}

	clear(mem[out : out+n*32])
	for i, index := range ready {
		sub := subs[index]
		b := mem[out+uint32(i)*32:]
		binary.LittleEndian.PutUint64(b, sub.userdata)
		binary.LittleEndian.PutUint16(b[8:], sub.code)
		b[10] = sub.typ
	}
	if !putLe32(mem, result, uint32(len(ready))) {
		r[0] = wasiEFault
		return
	}
	r[0] = wasiOK
}

func readySubscriptions(subs []pollSubscription, elapsed time.Duration) []int {
	ready := make([]int, 0, len(subs))
	for i := range subs {
		sub := &subs[i]
		if sub.clock {
			if elapsed >= sub.due {
				ready = append(ready, i)
			}
			continue
		}
		if sub.code != 0 || streamReady(sub.entry, sub.typ) {
			ready = append(ready, i)
		}
	}
	return ready
}

func streamObject(entry *fdEntry, typ byte) any {
	if entry == nil {
		return nil
	}
	if entry.file != nil {
		return entry.file
	}
	if typ == 1 {
		return entry.reader
	}
	return entry.writer
}

func streamReady(entry *fdEntry, typ byte) bool {
	object := streamObject(entry, typ)
	if object == nil {
		return true
	}
	if pollable, ok := object.(Pollable); ok {
		return pollable.Ready()
	}
	if typ == 2 {
		if _, ok := object.(interface{ Bytes() []byte }); ok {
			return true
		}
	}
	file, ok := object.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err == nil && info.Mode().IsRegular() {
		return true
	}
	return osFileReady(file, typ)
}

var errPollUnsupported = errors.New("wasi: stream does not implement readiness")

func (e *Plugin) waitSubscriptions(subs []pollSubscription, delay time.Duration, hasDeadline bool) error {
	ctx := e.cfg.Context
	if ctx == nil {
		ctx = context.Background()
	}
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if hasDeadline {
		var deadlineCancel context.CancelFunc
		waitCtx, deadlineCancel = context.WithTimeout(waitCtx, delay)
		defer deadlineCancel()
	}

	type waitResult struct{ err error }
	results := make(chan waitResult, len(subs)+1)
	hasWaiter := false
	var files []pollFile
	for i := range subs {
		sub := &subs[i]
		if sub.clock || sub.code != 0 {
			continue
		}
		object := streamObject(sub.entry, sub.typ)
		if pollable, ok := object.(Pollable); ok {
			hasWaiter = true
			go func() { results <- waitResult{err: pollable.Wait(waitCtx)} }()
			continue
		}
		if file, ok := object.(*os.File); ok {
			files = append(files, pollFile{file: file, typ: sub.typ})
			continue
		}
		if object != nil {
			if sub.typ == 2 {
				if _, ok := object.(interface{ Bytes() []byte }); ok {
					continue
				}
			}
			return errPollUnsupported
		}
	}
	if len(files) != 0 {
		hasWaiter = true
		go func() { results <- waitResult{err: waitOSFiles(waitCtx, files)} }()
	}
	if !hasWaiter {
		if hasDeadline {
			<-waitCtx.Done()
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return nil
			}
			return waitCtx.Err()
		}
		return errPollUnsupported
	}
	select {
	case result := <-results:
		if hasDeadline && errors.Is(result.err, context.DeadlineExceeded) {
			return nil
		}
		return result.err
	case <-waitCtx.Done():
		if hasDeadline && errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			return nil
		}
		return waitCtx.Err()
	}
}
