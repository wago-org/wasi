package p2

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wago-org/component-model"
	"github.com/wago-org/wago"
)

// These real components and assertions were ported from Wazy's WASI P2 suite.
// They exercise toolchain-produced components rather than synthetic ABI stubs.

//go:embed testdata/real_args.component.wasm
var upstreamRealArgsWasm []byte

//go:embed testdata/real_clocks.component.wasm
var upstreamRealClocksWasm []byte

//go:embed testdata/real_random.component.wasm
var upstreamRealRandomWasm []byte

//go:embed testdata/real_manyparams.component.wasm
var upstreamRealManyParamsWasm []byte

//go:embed testdata/real_resource.component.wasm
var upstreamRealResourceWasm []byte

func runUpstreamCommand(t *testing.T, wasm []byte, cfg Config) (string, string) {
	t.Helper()
	ctx := context.Background()
	rt := wago.NewRuntime()
	defer rt.Close()
	var stdout, stderr bytes.Buffer
	cfg.Stdout, cfg.Stderr = &stdout, &stderr
	components, err := Enable(rt, cfg)
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	inst, err := components.Instantiate(ctx, wasm)
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	defer inst.Close(ctx)
	results, err := inst.Call(ctx, "wasi:cli/run@0.2.3#run")
	if err != nil {
		t.Fatalf("Call run: %v (stdout=%q stderr=%q)", err, stdout.String(), stderr.String())
	}
	if len(results) != 1 {
		t.Fatalf("run returned %d results, want 1", len(results))
	}
	result, ok := results[0].(component.ResultValue)
	if !ok || result.IsErr {
		t.Fatalf("run result = %#v, want Ok", results[0])
	}
	return stdout.String(), stderr.String()
}

func TestUpstreamRealArgsAndEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		env  []string
		want []string
	}{
		{"first", []string{"hello", "from", "wago"}, []string{"GREETING=hi", "LANG=en"}, []string{"arg: hello\n", "arg: from\n", "arg: wago\n", "env: GREETING=hi\n", "env: LANG=en\n"}},
		{"different-input", []string{"second", "run"}, []string{"COLOR=blue"}, []string{"arg: second\n", "arg: run\n", "env: COLOR=blue\n"}},
		{"empty", nil, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := runUpstreamCommand(t, upstreamRealArgsWasm, Config{Args: tc.args, Env: tc.env})
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Fatalf("stdout %q does not contain %q", out, want)
				}
			}
			if tc.want == nil && (strings.Contains(out, "arg: ") || strings.Contains(out, "env: ")) {
				t.Fatalf("stdout = %q, want no argument or environment lines", out)
			}
		})
	}
}

func TestUpstreamRealClocks(t *testing.T) {
	for _, when := range []time.Time{time.Unix(1_700_000_000, 0), time.Unix(1_234_567_890, 0)} {
		when := when
		t.Run(fmt.Sprint(when.Unix()), func(t *testing.T) {
			start := time.Now()
			out, _ := runUpstreamCommand(t, upstreamRealClocksWasm, Config{WallClock: func() time.Time { return when }})
			if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
				t.Fatalf("guest sleep returned in %v, want at least 50ms", elapsed)
			}
			want := fmt.Sprintf("unix_secs=%d\nslept_at_least_50ms=true\n", when.Unix())
			if out != want {
				t.Fatalf("stdout = %q, want %q", out, want)
			}
		})
	}
}

func TestUpstreamRealRandom(t *testing.T) {
	out, _ := runUpstreamCommand(t, upstreamRealRandomWasm, Config{})
	want := "bytes_len=16\nu64_nonzero_likely=true\ninsecure_bytes_len=8\ninsecure_u64_ok=true\nseed_ok=true\n"
	if out != want {
		t.Fatalf("stdout = %q, want %q", out, want)
	}
}

func TestUpstreamCanonicalABISpillsTwentyParameters(t *testing.T) {
	ctx := context.Background()
	rt := wago.NewRuntime()
	defer rt.Close()
	components, err := Enable(rt, Config{})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := components.Instantiate(ctx, upstreamRealManyParamsWasm)
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(ctx)
	for _, tc := range []struct{ base, want uint32 }{{1, 210}, {100, 2190}} {
		args := make([]component.Value, 20)
		for i := range args {
			args[i] = tc.base + uint32(i)
		}
		got, err := inst.Call(ctx, "sum20", args...)
		if err != nil {
			t.Fatalf("sum20(%d): %v", tc.base, err)
		}
		if len(got) != 1 || got[0] != tc.want {
			t.Fatalf("sum20(%d) = %v, want %d", tc.base, got, tc.want)
		}
	}
}

func TestUpstreamGuestOwnedResources(t *testing.T) {
	ctx := context.Background()
	rt := wago.NewRuntime()
	defer rt.Close()
	components, err := Enable(rt, Config{})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := components.Instantiate(ctx, upstreamRealResourceWasm)
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(ctx)
	const iface = "example:res/counters"
	call := func(name string, args ...component.Value) component.Value {
		t.Helper()
		results, callErr := inst.CallExport(ctx, iface, name, args...)
		if callErr != nil || len(results) != 1 {
			t.Fatalf("%s = %v, %v", name, results, callErr)
		}
		return results[0]
	}
	h1 := call("make", uint32(10)).(uint32)
	h2 := call("make", uint32(100)).(uint32)
	if got := call("[method]counter.increment", h1); got != uint32(11) {
		t.Fatalf("increment = %v, want 11", got)
	}
	if got := call("[method]counter.add", h1, uint32(5)); got != uint32(16) {
		t.Fatalf("add = %v, want 16", got)
	}
	if got := call("[method]counter.increment", h2); got != uint32(101) {
		t.Fatalf("second increment = %v, want 101", got)
	}
	if got := call("sum-all", []component.Value{h1, h2}); got != uint32(117) {
		t.Fatalf("sum-all = %v, want 117", got)
	}
	if err := inst.DropResource(ctx, iface, "counter", h1); err != nil {
		t.Fatalf("drop first resource: %v", err)
	}
	if _, err := inst.CallExport(ctx, iface, "[method]counter.get", h1); err == nil {
		t.Fatal("use after resource drop succeeded")
	}
	if err := inst.DropResource(ctx, iface, "counter", h1); err == nil {
		t.Fatal("double resource drop succeeded")
	}
	if err := inst.DropResource(ctx, iface, "counter", h2); err != nil {
		t.Fatalf("drop second resource: %v", err)
	}
}
