// Package unstable exposes the Preview 1 ABI under the deprecated
// wasi_unstable import module name. It does not emulate earlier ABI snapshots.
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
	CapPathOpen        = core.CapPathOpen
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
type Preopen = core.Preopen
type ClockSource = core.ClockSource

func Definition() wago.PluginDefinition {
	return core.Definition(
		ID,
		"WASI unstable",
		"Deprecated Preview 1 ABI alias using the wasi_unstable import module name.",
		wago.Deprecated,
		Module,
	)
}

func Provider() wago.PluginProvider { return core.Provider(Definition(), Module) }

func Imports(cfg Config) wago.Imports { return core.Imports(Module, cfg) }
