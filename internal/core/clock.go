package core

import (
	"errors"
	"time"
)

var errUnsupportedClock = errors.New("unsupported clock")

// ClockSource supplies the four clocks named by WASI Preview 1. Implementations
// must keep Monotonic nondecreasing. CPU clocks may return an error when the
// platform cannot supply them rather than fabricating wall-clock values.
type ClockSource interface {
	Realtime() (now, resolution uint64, err error)
	Monotonic() (now, resolution uint64, err error)
	ProcessCPU() (now, resolution uint64, err error)
	ThreadCPU() (now, resolution uint64, err error)
}

type systemClock struct {
	base     time.Time
	realtime func() time.Time
}

func newSystemClock(realtime func() time.Time) ClockSource {
	if realtime == nil {
		realtime = time.Now
	}
	return &systemClock{base: time.Now(), realtime: realtime}
}

func (c *systemClock) Realtime() (uint64, uint64, error) {
	n := c.realtime()
	if n.UnixNano() < 0 {
		return 0, 1, nil
	}
	return uint64(n.UnixNano()), 1, nil
}

func (c *systemClock) Monotonic() (uint64, uint64, error) {
	return uint64(time.Since(c.base)), 1, nil
}

func (*systemClock) ProcessCPU() (uint64, uint64, error) {
	return 0, 0, errUnsupportedClock
}

func (*systemClock) ThreadCPU() (uint64, uint64, error) {
	return 0, 0, errUnsupportedClock
}

func clockValue(source ClockSource, id uint32) (uint64, uint64, error) {
	switch id {
	case 0:
		return source.Realtime()
	case 1:
		return source.Monotonic()
	case 2:
		return source.ProcessCPU()
	case 3:
		return source.ThreadCPU()
	default:
		return 0, 0, errors.New("invalid clock")
	}
}
