// Package wasi provides WASI Preview 1 for Wago under the standard
// wasi_snapshot_preview1 Wasm import module.
//
// Provider returns an explicit, side-effect-free vNext plugin catalog entry.
// Embedders that intentionally bypass the plugin policy can still use Imports
// with the low-level instantiator.
package wasi

import (
	wago "github.com/wago-org/wago"
	"github.com/wago-org/wasi/internal/core"
)

const (
	// ID is the canonical root plugin ID.
	ID = "github.com/wago-org/wasi"
	// Module is the standard Preview 1 Wasm import module.
	Module = "wasi_snapshot_preview1"

	CapFDRead          = core.CapFDRead
	CapFDWrite         = core.CapFDWrite
	CapFDManage        = core.CapFDManage
	CapPathRead        = core.CapPathRead
	CapPathWrite       = core.CapPathWrite
	CapArgumentsRead   = core.CapArgumentsRead
	CapEnvironmentRead = core.CapEnvironmentRead
	CapClockRead       = core.CapClockRead
	CapRandomRead      = core.CapRandomRead
	CapProcessExit     = core.CapProcessExit
	CapPoll            = core.CapPoll
	CapSchedulerYield  = core.CapSchedulerYield
	CapUnsupported     = core.CapUnsupported
)

// Config configures the raw Imports path. Plugin configuration is strict JSON
// recorded in Wago's reviewed lock graph; see README.md for its schema.
type Config = core.Config

// Definition returns fresh immutable metadata for the root Preview 1 provider.
func Definition() wago.PluginDefinition {
	return core.Definition(
		ID,
		"WASI",
		"WASI Preview 1 host functions with bounded filesystems, stdio, argv, environment, clocks, random, polling, and process exit.",
		wago.Stable,
		Module,
	)
}

// Provider returns the root package's side-effect-free catalog entry.
func Provider() wago.PluginProvider { return core.Provider(Definition(), Module) }

// Imports returns the raw Preview 1 host bundle for low-level instantiation.
func Imports(cfg Config) wago.Imports { return core.Imports(Module, cfg) }
