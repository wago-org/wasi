// Package register wires the default WASI Preview 1 plugin into the Wago
// engine's global plugin registry as a side effect of import. A custom Wago
// build includes WASI by
// blank-importing it:
//
//	import _ "github.com/wago-org/wasi/register"
//
// This is the generic plugin-registration convention: a plugin module ships a
// `register` package whose init() calls wago.RegisterExtension, so `wago plugin
// build` only has to blank-import it — no engine-side special-casing.
//
// Snapshot-specific registration shims live under p1/register, p2/register,
// and unstable/register. Keeping them separate prevents a Preview 1-only build
// from retaining the Component Model and Preview 2 implementation.
package register

import (
	"os"
	"time"

	wago "github.com/wago-org/wago"
	"github.com/wago-org/wasi"
)

func init() {
	// Host config: the wago process's own stdio and environment. Register obtains
	// the run's argv from Wago's capability-gated host environment.
	std := func() wasi.Config {
		return wasi.Config{
			Stdout: os.Stdout, Stderr: os.Stderr, Stdin: os.Stdin,
			Env: os.Environ(),
			Now: func() int64 { return time.Now().UnixNano() },
		}
	}
	wago.RegisterExtension("github.com/wago-org/wasi", func() wago.Extension { return wasi.Init(std()) })
}
