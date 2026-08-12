// Package p1 provides WASI Preview 1 for Wago under the standard
// wasi_snapshot_preview1 Wasm import module.
package p1

import (
	wago "github.com/wago-org/wago"
	"github.com/wago-org/wasi/internal/core"
)

const (
	ID     = "github.com/wago-org/wasi/p1"
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

type Config = core.Config

func Definition() wago.PluginDefinition {
	return core.Definition(
		ID,
		"WASI Preview 1",
		"The standard wasi_snapshot_preview1 command ABI for core WebAssembly modules.",
		wago.Stable,
		Module,
	)
}

func Provider() wago.PluginProvider { return core.Provider(Definition(), Module) }

func Imports(cfg Config) wago.Imports { return core.Imports(Module, cfg) }
