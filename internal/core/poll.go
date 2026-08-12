package core

import (
	"encoding/binary"
	"time"

	wago "github.com/wago-org/wago"
)

// pollOneoff implements the Preview 1 subscription/event ABI. Regular files
// and configured streams are always ready; relative clock subscriptions wait
// only when no descriptor event is ready.
func (e *Plugin) pollOneoff(m wago.HostModule, p, r []uint64) {
	in, out, n, result := uint32(p[0]), uint32(p[1]), uint32(p[2]), uint32(p[3])
	mem := m.Memory()
	if n == 0 {
		r[0] = wasiEInval
		return
	}
	if uint64(in)+uint64(n)*48 > uint64(len(mem)) || uint64(out)+uint64(n)*32 > uint64(len(mem)) {
		r[0] = wasiEFault
		return
	}
	type event struct {
		userdata uint64
		typ      byte
		code     uint16
	}
	events := make([]event, 0, n)
	clocks := make([]event, 0, n)
	var delay time.Duration
	maxDelay := e.cfg.MaxPollDuration
	if maxDelay <= 0 {
		maxDelay = time.Second
	}
	for i := uint32(0); i < n; i++ {
		sub := mem[in+i*48 : in+(i+1)*48]
		ev := event{userdata: binary.LittleEndian.Uint64(sub), typ: sub[8]}
		switch ev.typ {
		case 0: // clock
			clockID := binary.LittleEndian.Uint32(sub[16:])
			if clockID > 3 {
				r[0] = wasiEInval
				return
			}
			timeout := binary.LittleEndian.Uint64(sub[24:])
			flags := binary.LittleEndian.Uint16(sub[40:])
			if flags&^uint16(1) != 0 {
				r[0] = wasiEInval
				return
			}
			if flags&1 != 0 {
				now := uint64(time.Now().UnixNano())
				if timeout > now {
					timeout -= now
				} else {
					timeout = 0
				}
			}
			if timeout > uint64(maxDelay) {
				r[0] = wasiEInval
				return
			}
			if d := time.Duration(timeout); delay == 0 || d < delay {
				delay = d
			}
			clocks = append(clocks, ev)
		case 1, 2: // fd_read / fd_write
			fd := binary.LittleEndian.Uint32(sub[16:])
			f, code := e.entry(fd)
			if code == 0 {
				right := rightFDRead
				if ev.typ == 2 {
					right = rightFDWrite
				}
				code = require(f, uint64(right))
			}
			ev.code = uint16(code)
			events = append(events, ev)
		default:
			r[0] = wasiEInval
			return
		}
	}
	if len(events) == 0 {
		if delay > 0 {
			// withFS owns this lock while resolving descriptors. No filesystem
			// state is used after this point, so release it while waiting to keep
			// one guest clock from blocking unrelated WASI instances.
			e.guard.mu.Unlock()
			time.Sleep(delay)
			e.guard.mu.Lock()
		}
		events = append(events, clocks...)
	}
	clear(mem[out : out+n*32])
	for i, ev := range events {
		b := mem[out+uint32(i)*32:]
		binary.LittleEndian.PutUint64(b, ev.userdata)
		binary.LittleEndian.PutUint16(b[8:], ev.code)
		b[10] = ev.typ
	}
	if !putLe32(mem, result, uint32(len(events))) {
		r[0] = wasiEFault
		return
	}
	r[0] = wasiOK
}
