package p2

import (
	"bytes"
	"context"
	_ "embed"
	"testing"

	"github.com/wago-org/wago/src/wago"
)

//go:embed testdata/real_hello.component.wasm
var realHelloWasm []byte

func TestRealHello(t *testing.T) {
	ctx := context.Background()
	r := wago.NewRuntime()
	defer r.Close()
	components, err := Enable(r)
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}

	var stdout, stderr bytes.Buffer
	inst, err := components.Instantiate(ctx, realHelloWasm,
		With(Config{Stdout: &stdout, Stderr: &stderr})...)
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	defer inst.Close(ctx)
	t.Log("component instantiated")

	results, err := inst.Call(ctx, "wasi:cli/run@0.2.3#run")
	if err != nil {
		t.Fatalf("run: %v (stdout %q, stderr %q)", err, stdout.String(), stderr.String())
	}
	if got := stdout.String(); got != "hello world\n" {
		t.Fatalf("stdout = %q, want %q", got, "hello world\n")
	}
	if len(results) != 1 {
		t.Fatalf("run returned %d results, want 1", len(results))
	}
}
