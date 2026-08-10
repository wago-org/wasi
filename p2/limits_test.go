package p2

import (
	"bytes"
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/wago-org/component-model"
	sys "github.com/wago-org/wasi/internal/p2sys"
)

func TestReadAllLimited(t *testing.T) {
	t.Run("at limit", func(t *testing.T) {
		got, err := readAllLimited(strings.NewReader("abcd"), 4)
		if err != nil || !bytes.Equal(got, []byte("abcd")) {
			t.Fatalf("readAllLimited = %q, %v", got, err)
		}
	})
	t.Run("over limit", func(t *testing.T) {
		got, err := readAllLimited(strings.NewReader("abcde"), 4)
		if err == nil || got != nil {
			t.Fatalf("readAllLimited = %q, %v; want nil, error", got, err)
		}
	})
	t.Run("negative", func(t *testing.T) {
		if _, err := readAllLimited(strings.NewReader(""), -1); err == nil {
			t.Fatal("readAllLimited accepted a negative limit")
		}
	})
}

func TestWASINewTimestampNanosRejectsMalformedAndOverflow(t *testing.T) {
	if got, err := wasiNewTimestampNanos(component.VariantValue{Disc: 0}); err != nil || got != sys.UTIME_OMIT {
		t.Fatalf("no-change = %d, %v", got, err)
	}
	good := component.VariantValue{Disc: 2, Payload: []component.Value{uint64(7), uint32(11)}}
	if got, err := wasiNewTimestampNanos(good); err != nil || got != 7_000_000_011 {
		t.Fatalf("timestamp = %d, %v", got, err)
	}
	bad := []component.Value{
		component.VariantValue{Disc: 3},
		component.VariantValue{Disc: 2, Payload: []component.Value{uint64(math.MaxUint64), uint32(0)}},
		component.VariantValue{Disc: 2, Payload: []component.Value{uint64(0), uint32(1_000_000_000)}},
	}
	for _, value := range bad {
		if _, err := wasiNewTimestampNanos(value); err == nil {
			t.Fatalf("wasiNewTimestampNanos(%v) succeeded", value)
		}
	}
}

func TestPollableReadyTracksTimerDeadline(t *testing.T) {
	p := newWasiPoll(time.Now)
	rep := p.newTimer(time.Now().Add(time.Hour))
	got, err := p.ready(context.Background(), []component.Value{rep})
	if err != nil || len(got) != 1 || got[0] != false {
		t.Fatalf("ready before deadline = %v, %v", got, err)
	}
	p.mu.Lock()
	p.deadlines[rep] = time.Now().Add(-time.Nanosecond)
	p.mu.Unlock()
	got, err = p.ready(context.Background(), []component.Value{rep})
	if err != nil || len(got) != 1 || got[0] != true {
		t.Fatalf("ready after deadline = %v, %v", got, err)
	}
}

func TestTimezoneDefaultsToUTCWithoutHostDisclosure(t *testing.T) {
	p := newWasiPoll(time.Now)
	when := []component.Value{uint64(1_700_000_000), uint32(0)}
	got, err := p.timezoneDisplay(context.Background(), []component.Value{when})
	if err != nil {
		t.Fatal(err)
	}
	want := []component.Value{int32(0), "UTC", false}
	fields, ok := got[0].([]component.Value)
	if !ok || len(fields) != len(want) {
		t.Fatalf("timezone display = %#v", got)
	}
	for i := range want {
		if fields[i] != want[i] {
			t.Fatalf("timezone display field %d = %#v, want %#v", i, fields[i], want[i])
		}
	}
}

func TestHTTPFieldsRejectInjectionAndPreserveImmutability(t *testing.T) {
	h := newWasiHTTP()
	mutable := h.newFieldsRep(&httpFields{})
	for _, tc := range []struct {
		name  string
		value []byte
	}{{"bad\r\nname", []byte("ok")}, {"x-safe", []byte("value\r\ninjected: yes")}} {
		got, err := h.fieldsAppend(context.Background(), []component.Value{mutable, tc.name, tc.value})
		if err != nil {
			t.Fatal(err)
		}
		result, ok := got[0].(component.ResultValue)
		if !ok || !result.IsErr {
			t.Fatalf("append(%q, %q) = %#v; want header-error", tc.name, tc.value, got)
		}
	}

	immutable := h.newFieldsRep(&httpFields{immutable: true})
	got, err := h.fieldsDelete(context.Background(), []component.Value{immutable, "x-safe"})
	if err != nil {
		t.Fatal(err)
	}
	result := got[0].(component.ResultValue)
	variant := result.Payload.(component.VariantValue)
	if !result.IsErr || variant.Disc != httpHeaderErrorImmutable {
		t.Fatalf("immutable delete = %#v", got)
	}
}
