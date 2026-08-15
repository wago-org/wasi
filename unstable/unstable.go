// Package unstable provides the deprecated pre-Preview 1 wasi_unstable module
// name for older toolchains. Its syscall behavior matches package p1.
package unstable

import (
	wago "github.com/wago-org/wago"
	"github.com/wago-org/wasi/internal/core"
)

const (
	ID     = "github.com/wago-org/wasi/unstable"
	Module = "wasi_unstable"

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
		"WASI unstable",
		"Legacy wasi_unstable compatibility.",
		wago.Deprecated,
		Module,
	)
}

func Provider() wago.PluginProvider { return core.Provider(Definition(), Module) }

func Imports(cfg Config) wago.Imports { return core.Imports(Module, cfg) }
