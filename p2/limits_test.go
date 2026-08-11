package p2

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wago-org/component-model"
	"github.com/wago-org/wago"
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

func TestPollRejectsEmptyInput(t *testing.T) {
	p := newWasiPoll(time.Now)
	if _, err := p.poll(context.Background(), []component.Value{[]component.Value{}}); err == nil {
		t.Fatal("poll([]) succeeded; WASI requires a trap")
	}
}

func TestPollReadinessDistinguishesMultipleSources(t *testing.T) {
	p := newWasiPoll(time.Now)
	firstReady, secondReady := false, false
	first := p.newReadiness(func() bool { return firstReady })
	second := p.newReadiness(func() bool { return secondReady })
	if got := p.readyIndices([]uint32{first, second}); len(got) != 0 {
		t.Fatalf("ready indices with no data = %v", got)
	}
	secondReady = true
	got := p.readyIndices([]uint32{first, second})
	if len(got) != 1 || got[0] != uint32(1) {
		t.Fatalf("ready indices = %v, want [1]", got)
	}
}

func TestUnsignedTimerDurationsNeverWrapIntoThePast(t *testing.T) {
	base := time.Now()
	for _, nanos := range []uint64{math.MaxInt64, uint64(1) << 63, math.MaxUint64} {
		deadline := addUnsignedNanos(base, nanos)
		if !deadline.After(base) {
			t.Fatalf("addUnsignedNanos(base, %d) = %v; want future deadline", nanos, deadline)
		}
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

func TestWASIResourceTagsAreGloballyUnique(t *testing.T) {
	tags := map[string]uint32{
		"output-stream": wasiOutputStreamResType, "input-stream": wasiInputStreamResType,
		"error": wasiErrorResType, "descriptor": wasiDescriptorResType,
		"terminal-input": wasiTerminalInputResType, "terminal-output": wasiTerminalOutputResType,
		"directory-entry-stream": wasiDirEntryStreamResType, "network": wasiNetworkResType,
		"tcp-socket": wasiTCPSocketResType, "pollable": wasiPollableResType,
		"udp-socket": wasiUDPSocketResType, "incoming-datagram-stream": wasiIncomingDatagramStreamResType,
		"outgoing-datagram-stream": wasiOutgoingDatagramStreamResType, "resolve-address-stream": wasiResolveStreamResType,
		"http-incoming-request": wasiHTTPIncomingRequestResType, "http-fields": wasiHTTPFieldsResType,
		"http-outgoing-response": wasiHTTPOutgoingResponseResType, "http-outgoing-body": wasiHTTPOutgoingBodyResType,
		"http-response-outparam": wasiHTTPResponseOutparamResType, "http-outgoing-request": wasiHTTPOutgoingRequestResType,
		"http-future": wasiHTTPFutureResType, "http-incoming-response": wasiHTTPIncomingResponseResType,
		"http-incoming-body": wasiHTTPIncomingBodyResType, "http-request-options": wasiHTTPRequestOptionsResType,
		"http-future-trailers": wasiHTTPFutureTrailersResType,
	}
	seen := map[uint32]string{}
	for name, tag := range tags {
		if prior, ok := seen[tag]; ok {
			t.Fatalf("resource tag %d is shared by %s and %s", tag, prior, name)
		}
		seen[tag] = name
	}
}

func TestHTTPBodyStreamsEnforceCumulativeLimit(t *testing.T) {
	h := newWasiHTTP()
	h.maxBodyBytes = 4
	for _, kind := range []string{"response", "request"} {
		t.Run(kind, func(t *testing.T) {
			body := &httpOutgoingBody{}
			rep := h.newBodyStreamRep(body)
			if err := validateFlushableOutputStream(rep, func(uint32) (io.Writer, error) {
				return nil, errors.New("not stdio")
			}, newWasiFS(nil), newWasiSockets(nil, nil, nil, nil), h); err != nil {
				t.Fatalf("flush live HTTP body stream: %v", err)
			}
			if found, err := h.bodyStreamWrite(rep, []byte("abc")); !found || err != nil {
				t.Fatalf("first write: found=%v err=%v", found, err)
			}
			if found, remaining, err := h.bodyStreamCapacity(rep); !found || err != nil || remaining != 1 {
				t.Fatalf("capacity: found=%v remaining=%d err=%v", found, remaining, err)
			}
			if found, err := h.bodyStreamWrite(rep, []byte("de")); !found || err == nil {
				t.Fatalf("over-limit write: found=%v err=%v", found, err)
			}
			if got := body.buf.String(); got != "abc" {
				t.Fatalf("body after rejected write = %q", got)
			}
			if found, err := h.bodyStreamWrite(rep, []byte("d")); !found || err != nil {
				t.Fatalf("write to exact limit: found=%v err=%v", found, err)
			}
			if found, remaining, err := h.bodyStreamCapacity(rep); !found || err == nil || remaining != 0 {
				t.Fatalf("capacity at limit: found=%v remaining=%d err=%v", found, remaining, err)
			}
		})
	}
}

func TestResourceDestructorsRemoveHostState(t *testing.T) {
	fs := newWasiFS(nil)
	fs.descs[7] = &fsDescNode{}
	fs.dropResource(wasiDescriptorResType, 7)
	if _, ok := fs.descs[7]; ok {
		t.Fatal("descriptor survived resource destructor")
	}

	h := newWasiHTTP()
	h.fields[9] = &httpFields{}
	h.dropResource(wasiHTTPFieldsResType, 9)
	if _, ok := h.fields[9]; ok {
		t.Fatal("HTTP fields survived resource destructor")
	}
}

func TestWithWASIOptionsCreateFreshStatePerInstance(t *testing.T) {
	ctx := context.Background()
	runtime := wago.NewRuntime()
	defer runtime.Close()
	components, err := component.Enable(runtime)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"before"}
	var stdout bytes.Buffer
	opts := WithWASI(Config{EnableHTTP: true, Args: args, Stdout: &stdout})
	args[0] = "after"
	first, err := components.Instantiate(ctx, upstreamRealArgsWasm, opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close(ctx)
	second, err := components.Instantiate(ctx, upstreamRealArgsWasm, opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(ctx)
	firstHost, secondHost := httpHostOf(first), httpHostOf(second)
	if firstHost == nil || secondHost == nil || firstHost == secondHost {
		t.Fatalf("HTTP host states = %p and %p; want distinct non-nil state", firstHost, secondHost)
	}
	firstRep := firstHost.newFieldsRep(&httpFields{})
	if len(secondHost.fields) != 0 {
		t.Fatal("resource state leaked between component instances")
	}
	secondRep := secondHost.newFieldsRep(&httpFields{})
	firstHost.dropResource(wasiHTTPFieldsResType, firstRep)
	if _, ok := secondHost.fields[secondRep]; !ok {
		t.Fatal("destructor in first instance removed second instance resource")
	}
	if _, err := first.Call(ctx, "wasi:cli/run@0.2.3#run"); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); !strings.Contains(got, "arg: before\n") || strings.Contains(got, "arg: after\n") {
		t.Fatalf("deferred options observed mutated args: %q", got)
	}
}

func TestHTTPHandlerSerializesOverlappingRequests(t *testing.T) {
	var active atomic.Int32
	var maximum atomic.Int32
	arrived := make(chan struct{}, 2)
	entered := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	callMu := new(sync.Mutex)
	serve := func(w http.ResponseWriter, _ *http.Request) {
		now := active.Add(1)
		for old := maximum.Load(); now > old && !maximum.CompareAndSwap(old, now); old = maximum.Load() {
		}
		entered <- struct{}{}
		<-release
		active.Add(-1)
		w.WriteHeader(http.StatusNoContent)
	}
	newServer := func() *httptest.Server {
		gated := serializedHTTPHandler(callMu, serve)
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			arrived <- struct{}{}
			gated.ServeHTTP(w, r)
		}))
	}
	firstServer := newServer()
	defer firstServer.Close()
	secondServer := newServer()
	defer secondServer.Close()

	var wg sync.WaitGroup
	request := func(server *httptest.Server) {
		defer wg.Done()
		resp, err := server.Client().Get(server.URL)
		if err == nil {
			resp.Body.Close()
		}
	}
	wg.Add(1)
	go request(firstServer)
	<-arrived
	<-entered
	wg.Add(1)
	go request(secondServer)
	<-arrived
	if got := maximum.Load(); got != 1 {
		t.Fatalf("maximum concurrent component calls = %d, want 1", got)
	}
	release <- struct{}{}
	<-entered
	release <- struct{}{}
	wg.Wait()
}

func TestWritePermitIsOneShotAndBounded(t *testing.T) {
	p := newWritePermitTable()
	if err := p.consume(1, 0); err == nil {
		t.Fatal("write without check-write permit succeeded")
	}
	p.set(1, 3)
	if err := p.consume(1, 4); err == nil {
		t.Fatal("write exceeding permit succeeded")
	}
	if err := p.consume(1, 1); err == nil {
		t.Fatal("failed write did not consume one-shot permit")
	}
	p.set(1, 3)
	if err := p.consume(1, 3); err != nil {
		t.Fatalf("write within permit failed: %v", err)
	}
	if err := p.consume(1, 0); err == nil {
		t.Fatal("permit was reusable")
	}
	p.set(2, 1)
	p.drop(2)
	if err := p.consume(2, 1); err == nil {
		t.Fatal("dropped stream retained its permit")
	}
}

func TestUDPSendPermitIsOneShotAndBounded(t *testing.T) {
	stream := &outgoingDatagramStream{}
	if err := stream.consumePermit(0); err == nil {
		t.Fatal("send without check-send permit succeeded")
	}
	stream.setPermit(2)
	if err := stream.consumePermit(3); err == nil {
		t.Fatal("send exceeding permit succeeded")
	}
	if err := stream.consumePermit(1); err == nil {
		t.Fatal("failed send did not consume one-shot permit")
	}
	stream.setPermit(2)
	if err := stream.consumePermit(2); err != nil {
		t.Fatalf("send within permit failed: %v", err)
	}
	if err := stream.consumePermit(0); err == nil {
		t.Fatal("send permit was reusable")
	}
}

func TestSocketReadIsNonblockingAndReadinessTracksData(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	stream := &sockInStream{conn: client}
	start := time.Now()
	result, err := stream.read(16, false)
	if err != nil || time.Since(start) > time.Second {
		t.Fatalf("nonblocking read = %v, %v", result, err)
	}
	if stream.ready() {
		t.Fatal("empty stream reported ready")
	}
	go func() { _, _ = server.Write([]byte("ready")) }()
	deadline := time.Now().Add(time.Second)
	for !stream.ready() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !stream.ready() {
		t.Fatal("stream did not become ready after write")
	}
	result, err = stream.read(16, false)
	if err != nil || string(result[0].(component.ResultValue).Payload.([]byte)) != "ready" {
		t.Fatalf("ready read = %#v, %v", result, err)
	}
}

func TestUDPReceiveIsNonblockingWithoutData(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	stream := &incomingDatagramStream{pconn: conn}
	start := time.Now()
	result, err := stream.receive(1)
	if err != nil || time.Since(start) > time.Second {
		t.Fatalf("nonblocking receive = %#v, %v", result, err)
	}
	rv := result[0].(component.ResultValue)
	if rv.IsErr || len(rv.Payload.([]component.Value)) != 0 {
		t.Fatalf("empty receive = %#v", result)
	}
}
