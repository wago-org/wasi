//go:build linux && amd64 && !tinygo

package p1_test

import (
	"bytes"
	_ "embed"
	"errors"
	"strings"
	"testing"
	"time"

	wago "github.com/wago-org/wago"
	"github.com/wago-org/wasi/p1"
)

// Built from testdata/rust-smoke with rustc 1.97.1's wasm32-wasip1 target.
//
//go:embed testdata/rust_smoke.wasm
var rustSmokeWasm []byte

type fixedClock struct{ realtime uint64 }

func (c fixedClock) Realtime() (uint64, uint64, error) { return c.realtime, 1, nil }
func (fixedClock) Monotonic() (uint64, uint64, error)  { return 1, 1, nil }
func (fixedClock) ProcessCPU() (uint64, uint64, error) { return 0, 0, errors.ErrUnsupported }
func (fixedClock) ThreadCPU() (uint64, uint64, error)  { return 0, 0, errors.ErrUnsupported }

func TestRustWASIP1CommandRunsRepeatedlyOnWago(t *testing.T) {
	compiled, err := wago.Compile(nil, rustSmokeWasm)
	if err != nil {
		t.Fatalf("compile Rust wasm32-wasip1 module: %v", err)
	}
	defer compiled.Close()

	for iteration := range 10 {
		var stdout, stderr bytes.Buffer
		instance, err := wago.Instantiate(compiled, wago.InstantiateOptions{Imports: p1.Imports(p1.Config{
			Stdin:  strings.NewReader("from-rust-stdin\n"),
			Stdout: &stdout,
			Stderr: &stderr,
			Args:   []string{"rust-smoke", "alpha", "beta"},
			Env:    []string{"WAGO_FLAVOR=core"},
			Clocks: fixedClock{realtime: uint64(time.Unix(1_700_000_000, 0).UnixNano())},
		})})
		if err != nil {
			t.Fatalf("iteration %d: instantiate: %v", iteration, err)
		}
		_, invokeErr := instance.Invoke("_start")
		closeErr := instance.Close()
		if invokeErr != nil {
			var exit *wago.ExitError
			if !errors.As(invokeErr, &exit) || exit.Code != 0 {
				t.Fatalf("iteration %d: invoke: %v", iteration, invokeErr)
			}
		}
		if closeErr != nil {
			t.Fatalf("iteration %d: close: %v", iteration, closeErr)
		}
		if got, want := stdout.String(), "args=alpha,beta;env=core;stdin=from-rust-stdin;map=2;clock=true\n"; got != want {
			t.Fatalf("iteration %d: stdout = %q, want %q", iteration, got, want)
		}
		if got, want := stderr.String(), "rust-wasip1-stderr\n"; got != want {
			t.Fatalf("iteration %d: stderr = %q, want %q", iteration, got, want)
		}
	}
}
