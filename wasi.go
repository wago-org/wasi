// Package wasi is the default WASI host interface for wasm guests: it provides
// wasi_snapshot_preview1 (the ABI emitted by wasm32-wasip1 toolchains), the common
// case. Importing this package gives you plain wasi.Init / wasi.Imports / wasi.Config.
//
// To pin a specific snapshot, import the versioned subpackage by its full path
// instead:
//
//	github.com/wago-org/wasi/p1        — wasi_snapshot_preview1 (same as this package)
//	github.com/wago-org/wasi/unstable  — wasi_unstable, the pre-preview1 "snapshot 0" ABI
//	github.com/wago-org/wasi/p2        — WASI preview 2 (component model; placeholder, not yet)
//
// Usage:
//
//	// On a Runtime (capability-gated, inspectable):
//	rt := wago.NewRuntime()
//	rt.Use(wasi.Init(wasi.Config{Stdout: os.Stdout}))
//
//	// As a raw host-import bundle on the low-level Instantiate path:
//	in, _ := wago.Instantiate(c, wasi.Imports(wasi.Config{Stdout: os.Stdout}))
//	in.Invoke("_start")
//
// Extension identity (version, stability, keywords, engines) lives in one place —
// wago.json at the module root — and is loaded via [Info], keyed by module path.
// wago.json is self-similar: every subpackage entry is itself a wago.json config
// (inline, or a "./path/wago.json" string), and it doubles as the manifest a
// registry reads, so runtime identity and the catalog never drift.
package wasi

import (
	"embed"
	"encoding/json"
	"strings"

	wago "github.com/wago-org/wago"
	"github.com/wago-org/wasi/internal/core"
)

// Module is the default WASI wasm import module name (wasi_snapshot_preview1).
const Module = "wasi_snapshot_preview1"

// Cap is the capability guarding the WASI surface.
const Cap = core.Cap

// Config configures the WASI host bundle. See the core package for field semantics.
type Config = core.Config

// files embeds wago.json plus any wago.json referenced by a subpackage path.
//
//go:embed wago.json p2/wago.json
var files embed.FS

// pkg is a wago.json config. The same shape is used at the module root and for
// every subpackage — a subpackage is just a nested module. Keyed by Module; there
// is no separate id.
type pkg struct {
	Schema      string            `json:"schema,omitempty"`
	Module      string            `json:"module"`
	Name        string            `json:"name,omitempty"`
	Version     string            `json:"version,omitempty"`
	Description string            `json:"description,omitempty"`
	Stability   string            `json:"stability,omitempty"`
	License     string            `json:"license,omitempty"`
	Homepage    string            `json:"homepage,omitempty"`
	Repository  string            `json:"repository,omitempty"`
	Authors     []string          `json:"authors,omitempty"`
	Keywords    []string          `json:"keywords,omitempty"`
	Engines     map[string]string `json:"engines,omitempty"`
	Platforms   []string          `json:"platforms,omitempty"`
	Private     bool              `json:"private,omitempty"`
	Subpackages []subref          `json:"subpackages,omitempty"`
}

// subref is a subpackages[] element: an inline pkg, or a "./path/wago.json" string
// pointing to another config file.
type subref struct {
	inline *pkg
	path   string
}

func (s *subref) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &s.path)
	}
	var p pkg
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	s.inline = &p
	return nil
}

// byModule maps a module path to its resolved config (provenance + engines
// inherited from the parent where a subpackage leaves them unset).
var byModule = map[string]pkg{}

func init() {
	index(load("wago.json"), pkg{})
}

func load(path string) pkg {
	b, err := files.ReadFile(strings.TrimPrefix(path, "./"))
	if err != nil {
		panic("wasi: reading " + path + ": " + err.Error())
	}
	var p pkg
	if err := json.Unmarshal(b, &p); err != nil {
		panic("wasi: parsing " + path + ": " + err.Error())
	}
	return p
}

// index records p (with inherited-from-parent defaults) and recurses into its
// subpackages, resolving any "./path/wago.json" references.
func index(p, parent pkg) {
	if p.License == "" {
		p.License = parent.License
	}
	if p.Homepage == "" {
		p.Homepage = parent.Homepage
	}
	if p.Repository == "" {
		p.Repository = parent.Repository
	}
	if len(p.Authors) == 0 {
		p.Authors = parent.Authors
	}
	if len(p.Engines) == 0 {
		p.Engines = parent.Engines
	}
	if len(p.Platforms) == 0 {
		p.Platforms = parent.Platforms
	}
	byModule[p.Module] = p
	for _, s := range p.Subpackages {
		child := s.inline
		if child == nil {
			c := load(s.path)
			child = &c
		}
		index(*child, p)
	}
}

// Info returns the [wago.ExtensionInfo] for the subpackage with the given module
// path, read from wago.json. It panics if no config declares that module — a
// mismatch between the code and the manifest is a build-time bug.
func Info(module string) wago.ExtensionInfo {
	p, ok := byModule[module]
	if !ok {
		panic("wasi: no module " + module + " in wago.json")
	}
	return wago.ExtensionInfo{
		ID:          p.Module,
		Name:        p.Name,
		Version:     p.Version,
		Description: p.Description,
		Stability:   wago.Stability(p.Stability),
		Homepage:    p.Homepage,
		Repository:  p.Repository,
		License:     p.License,
		Authors:     p.Authors,
		Tags:        p.Keywords,
		Private:     p.Private,
		Compat:      wago.Compatibility{Engines: p.Engines, Platforms: p.Platforms},
	}
}

// Init constructs the default (wasi_snapshot_preview1) extension from cfg.
func Init(cfg Config) wago.Extension {
	return core.New(Module, Info("github.com/wago-org/wasi/p1"), cfg)
}

// Imports returns the default (wasi_snapshot_preview1) host bundle for the
// low-level wago.Instantiate(c, imports) path.
func Imports(cfg Config) wago.Imports { return core.Imports(Module, cfg) }
