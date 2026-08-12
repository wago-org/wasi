//go:build linux && amd64 && !tinygo

// This WASI-suite harness uses t.Skip/t.Fatal and os/filepath, none of which
// behave under TinyGo, so it is excluded there (like the spec-suite harness).

package p1_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	wago "github.com/wago-org/wago"
	"github.com/wago-org/wasi/p1"
)

// wasiManifest mirrors a WebAssembly/wasi-testsuite per-test `.json`. All fields
// are optional; a missing file means the defaults (exit_code 0, no stdout check).
type wasiManifest struct {
	Args     []string          `json:"args"`
	Env      map[string]string `json:"env"`
	Root     string            `json:"root"`
	Ops      json.RawMessage   `json:"operations"` // presence ⇒ socket/interactive (wasip3)
	ExitCode int               `json:"exit_code"`
	Stdout   string            `json:"stdout"`
}

// TestWASISuite runs the WebAssembly/wasi-testsuite preview1 tests (the submodule
// at tests/wasi) through this WASI host bundle as a conformance oracle for the sync host-call
// path. Gated on WAGO_WASITEST_DIR (a checked-out wasi-testsuite). Every P1 test
// must run; a manifest root is preopened as fd 3 by runOneWASITest.
func TestWASISuite(t *testing.T) {
	dir := os.Getenv("WAGO_WASITEST_DIR")
	if dir == "" {
		t.Skip("set WAGO_WASITEST_DIR to a checked-out WebAssembly/wasi-testsuite to run")
	}
	var wasms []string
	for _, lang := range []string{"assemblyscript", "c", "rust"} {
		files, _ := filepath.Glob(filepath.Join(dir, "tests", lang, "testsuite", "wasm32-wasip1", "*.wasm"))
		wasms = append(wasms, files...)
	}
	if len(wasms) == 0 {
		t.Fatalf("no wasm32-wasip1 tests under %s (submodule checked out?)", dir)
	}
	sort.Strings(wasms)

	var pass, fail, skip int
	for _, wasmPath := range wasms {
		name := strings.TrimSuffix(filepath.Base(wasmPath), ".wasm")
		man := loadWASIManifest(strings.TrimSuffix(wasmPath, ".wasm") + ".json")
		if man.Ops != nil {
			skip++
			continue
		}
		if reason := runOneWASITest(wasmPath, man); reason != "" {
			fail++
			t.Errorf("%-36s FAIL: %s", name, reason)
		} else {
			pass++
			t.Logf("%-36s pass", name)
		}
	}
	t.Logf("TOTAL[wasip1]: passed=%d failed=%d skipped=%d (of %d)", pass, fail, skip, len(wasms))
	if pass == 0 {
		t.Fatal("no wasi tests passed — the suite did not actually run")
	}
}

func loadWASIManifest(path string) wasiManifest {
	var m wasiManifest
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// runOneWASITest runs one module and returns "" on success or a failure reason.
func runOneWASITest(wasmPath string, man wasiManifest) string {
	src, err := os.ReadFile(wasmPath)
	if err != nil {
		return err.Error()
	}
	c, err := wago.Compile(nil, src)
	if err != nil {
		return "compile: " + err.Error()
	}
	env := make([]string, 0, len(man.Env))
	for k, v := range man.Env {
		env = append(env, k+"="+v)
	}
	sort.Strings(env)
	// Guest argv is [program name, manifest args...] — the reference adapters pass
	// the module path as argv[0] followed by the test's args.
	args := append([]string{filepath.Base(wasmPath)}, man.Args...)
	cfg := p1.Config{Args: args, Env: env}
	if man.Root != "" {
		tmp, err := os.MkdirTemp("", "wago-wasi-p1-")
		if err != nil {
			return "temp root: " + err.Error()
		}
		defer os.RemoveAll(tmp)
		srcRoot := filepath.Join(filepath.Dir(wasmPath), man.Root)
		if err = copyTree(srcRoot, tmp); err != nil {
			return "copy root: " + err.Error()
		}
		cfg.Preopens = map[string]string{"/": tmp}
	}
	var stdout, stderr bytes.Buffer
	cfg.Stdout = &stdout
	cfg.Stderr = &stderr
	in, err := wago.Instantiate(c, wago.InstantiateOptions{Imports: p1.Imports(cfg)})
	if err != nil {
		return "instantiate: " + err.Error()
	}
	defer in.Close()

	code := 0
	if _, err := in.Invoke("_start"); err != nil {
		var ex *wago.ExitError
		if !errors.As(err, &ex) {
			return "trap: " + err.Error() + "; stderr: " + strings.TrimSpace(stderr.String())
		}
		code = int(ex.Code)
	}
	if code != man.ExitCode {
		return fmt.Sprintf("exit code %d, want %d", code, man.ExitCode)
	}
	if man.Stdout != "" && stdout.String() != man.Stdout {
		return fmt.Sprintf("stdout %q, want %q", stdout.String(), man.Stdout)
	}
	return ""
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if entry.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}
