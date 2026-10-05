package core

import (
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
