// Package wasi provides an experimental WASI provider bundle for Wago.
//
// Provider selects the Preview 1 and Preview 2 providers. Embedders that
// intentionally bypass plugin policy can still use Imports for low-level
// Preview 1 instantiation.
package wasi

import (
	wago "github.com/wago-org/wago"
	"github.com/wago-org/wasi/internal/core"
)

const (
	// ID is the canonical bundle plugin ID.
	ID = "github.com/wago-org/wasi"
	// Module is the standard Preview 1 Wasm import module.
	Module = "wasi_snapshot_preview1"

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

// Config configures the raw Imports path. Plugin configuration is strict JSON
// recorded in Wago's reviewed lock graph; see README.md for its schema.
type Config = core.Config
type Preopen = core.Preopen
type ClockSource = core.ClockSource

// Definition returns fresh immutable metadata for the complete WASI bundle.
func Definition() wago.PluginDefinition {
	return wago.PluginDefinition{
		ID:          ID,
		Name:        "WASI",
		Version:     "0.3.0",
		Description: "Experimental bundle of Preview 1 and WASI 0.2 command imports.",
		Stability:   wago.Experimental,
		Compatibility: wago.Compatibility{
			Engines:   map[string]string{"wago": ">=0.1.0", "go": ">=1.22"},
			Platforms: []string{"darwin/arm64", "linux/amd64", "linux/arm64"},
		},
		Provenance: wago.PluginProvenance{
			Homepage:   "https://github.com/wago-org/wasi",
			Repository: "https://github.com/wago-org/wasi",
			License:    "Apache-2.0",
			Authors:    []string{"The Wago authors"},
		},
		Requires: []wago.PluginRequirement{
			{ID: "github.com/wago-org/wasi/p1", Version: "^0.3.0"},
			{ID: "github.com/wago-org/wasi/p2", Version: "^0.3.0"},
		},
	}
}

type bundlePlugin struct{}

func (bundlePlugin) Register(*wago.Registrar) error { return nil }

// Provider returns the bundle's side-effect-free catalog entry.
func Provider() wago.PluginProvider {
	return wago.PluginProvider{
		Definition: Definition(),
		New:        func() wago.Plugin { return bundlePlugin{} },
	}
}

// Imports returns the raw Preview 1 host bundle for low-level instantiation.
func Imports(cfg Config) wago.Imports { return core.Imports(Module, cfg) }
