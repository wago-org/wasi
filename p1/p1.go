// Package p1 provides the wasi_snapshot_preview1 host interface as a wago plugin.
// This is the common WASI ABI emitted by wasm32-wasip1 toolchains (Rust, C, Go,
// AssemblyScript): enough for programs that read/write the standard streams, exit,
// and query args/env/clock/random.
//
// Two ways to use it:
//
//	// As a plugin on a Runtime (capability-gated, inspectable):
//	rt := wago.NewRuntime()
//	rt.Use(p1.Init(p1.Config{Stdout: os.Stdout}))
//
//	// As a raw host-import bundle on the low-level Instantiate path:
//	in, _ := wago.Instantiate(c, p1.Imports(p1.Config{Stdout: os.Stdout}))
//	in.Invoke("_start")
package p1

import (
	wago "github.com/wago-org/wago"
	"github.com/wago-org/wasi"
	"github.com/wago-org/wasi/internal/core"
)

// ID is this extension's module path — its key in the module's wago.json.
const ID = "github.com/wago-org/wasi/p1"

// Module is the wasm import module name these functions bind under.
const Module = "wasi_snapshot_preview1"

// Cap is the capability guarding the WASI surface.
const Cap = core.Cap

// Config configures the host bundle. See core.Config for field semantics.
type Config = core.Config

// Init constructs the wasi_snapshot_preview1 extension from cfg; its identity is
// loaded from the module's wago.json.
func Init(cfg Config) wago.Extension {
	return core.New(Module, wasi.Info(ID), cfg)
}

// Imports returns the wasi_snapshot_preview1 host bundle for the low-level
// wago.Instantiate(c, imports) path, keyed "wasi_snapshot_preview1.<name>".
func Imports(cfg Config) wago.Imports { return core.Imports(Module, cfg) }
