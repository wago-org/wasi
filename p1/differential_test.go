//go:build !tinygo

package p1_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	wago "github.com/wago-org/wago"
	"github.com/wago-org/wasi/p1"
)

type observation struct {
	stdout, stderr string
	exit           uint32
}

func TestPreview1DifferentialSmoke(t *testing.T) {
	wasm, err := os.ReadFile("testdata/rust_smoke.wasm")
	if err != nil {
		t.Fatal(err)
	}
	wagoResult := runWago(t, wasm)
	wazeroResult := runWazero(t, wasm)
	if wagoResult != wazeroResult {
		t.Fatalf("Wago/Wazero mismatch\nWago:  %#v\nWazero:%#v", wagoResult, wazeroResult)
	}
	wasmtime := os.Getenv("WASMTIME")
	if wasmtime == "" {
		t.Log("WASMTIME unset; external differential is enforced in CI")
		return
	}
	wasmtimeResult := runWasmtime(t, wasmtime)
	if wagoResult != wasmtimeResult {
		t.Fatalf("Wago/Wasmtime mismatch\nWago:    %#v\nWasmtime:%#v", wagoResult, wasmtimeResult)
	}
}

func runWago(t *testing.T, wasm []byte) observation {
	t.Helper()
	compiled, err := wago.Compile(nil, wasm)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	in, err := wago.Instantiate(compiled, wago.InstantiateOptions{Imports: p1.Imports(p1.Config{Stdin: strings.NewReader("from-rust-stdin\n"), Stdout: &stdout, Stderr: &stderr, Args: []string{"wago", "alpha", "beta"}, Env: []string{"WAGO_FLAVOR=component"}})})
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	var exit uint32
	if _, err = in.Invoke("_start"); err != nil {
		var ex *wago.ExitError
		if !errors.As(err, &ex) {
			t.Fatal(err)
		}
		exit = uint32(ex.Code)
	}
	return observation{stdout.String(), stderr.String(), exit}
}

func runWazero(t *testing.T, wasm []byte) observation {
	t.Helper()
	ctx := context.Background()
	runtime := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfigCompiler())
	defer runtime.Close(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, runtime)
	compiled, err := runtime.CompileModule(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	defer compiled.Close(ctx)
	var stdout, stderr bytes.Buffer
	cfg := wazero.NewModuleConfig().WithArgs("wago", "alpha", "beta").WithEnv("WAGO_FLAVOR", "component").WithStdin(strings.NewReader("from-rust-stdin\n")).WithStdout(&stdout).WithStderr(&stderr)
	_, err = runtime.InstantiateModule(ctx, compiled, cfg)
	var exit uint32
	if err != nil {
		var ex interface{ ExitCode() uint32 }
		if !errors.As(err, &ex) {
			t.Fatal(err)
		}
		exit = ex.ExitCode()
	}
	return observation{stdout.String(), stderr.String(), exit}
}

func runWasmtime(t *testing.T, binary string) observation {
	t.Helper()
	cmd := exec.Command(binary, "run", "--env", "WAGO_FLAVOR=component", "--argv0", "wago", "testdata/rust_smoke.wasm", "alpha", "beta")
	cmd.Stdin = strings.NewReader("from-rust-stdin\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit uint32
	if err != nil {
		var ex *exec.ExitError
		if !errors.As(err, &ex) {
			t.Fatal(err)
		}
		exit = uint32(ex.ExitCode())
	}
	return observation{stdout.String(), stderr.String(), exit}
}
