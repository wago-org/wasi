package core

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

func TestRealtimeAfterUnixNanoRange(t *testing.T) {
	instant := time.Date(2300, time.January, 1, 0, 0, 0, 123, time.UTC)
	clock := newSystemClock(func() time.Time { return instant })
	got, resolution, err := clock.Realtime()
	if err != nil {
		t.Fatal(err)
	}
	want := uint64(instant.Unix())*1_000_000_000 + uint64(instant.Nanosecond())
	if got != want || resolution != 1 {
		t.Fatalf("Realtime() = (%d, %d), want (%d, 1)", got, resolution, want)
	}
}

func TestRealtimeBeyondWASITimestampRange(t *testing.T) {
	clock := newSystemClock(func() time.Time {
		return time.Date(3000, time.January, 1, 0, 0, 0, 0, time.UTC)
	})
	if _, _, err := clock.Realtime(); !errors.Is(err, errClockOverflow) {
		t.Fatalf("Realtime() error = %v, want clock overflow", err)
	}
	e := newTestPlugin(t, Config{Clocks: clock})
	result := make([]uint64, 1)
	mem := make([]byte, 256)
	e.clockTimeGet(testModule{mem: mem}, []uint64{0, 0, 0}, result)
	if result[0] != wasiEOverflow {
		t.Fatalf("clock_time_get errno = %d, want overflow", result[0])
	}
	e.clockResGet(testModule{mem: mem}, []uint64{0, 8}, result)
	if result[0] != wasiOK || binary.LittleEndian.Uint64(mem[8:]) != 1 {
		t.Fatalf("clock_res_get = (%d, %d), want (OK, 1)", result[0], binary.LittleEndian.Uint64(mem[8:]))
	}
	putClockSubscription(mem, 0, 1, 0, 0, false)
	e.pollOneoff(testModule{mem: mem}, []uint64{0, 64, 1, 128}, result)
	if result[0] != wasiOK || binary.LittleEndian.Uint32(mem[128:]) != 1 {
		t.Fatalf("relative poll = (%d, %d events), want (OK, 1)", result[0], binary.LittleEndian.Uint32(mem[128:]))
	}
	putClockSubscription(mem, 0, 1, 0, 0, true)
	e.pollOneoff(testModule{mem: mem}, []uint64{0, 64, 1, 128}, result)
	if result[0] != wasiEOverflow {
		t.Fatalf("absolute poll errno = %d, want overflow", result[0])
	}
}
