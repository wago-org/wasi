// Package register wires the WASI plugins into the wago engine's global plugin
// registry as a side effect of import. A custom wago build includes WASI by
// blank-importing it:
//
//	import _ "github.com/wago-org/wasi/register"
//
// This is the generic plugin-registration convention: a plugin module ships a
// `register` package whose init() calls wago.RegisterExtension, so `wago plugin
// build` only has to blank-import it — no engine-side special-casing.
//
// It lives in its own leaf package (not the module root) because it imports the
// p1/unstable subpackages, which import the root for their manifest metadata; a
// root-level init() would create an import cycle.
package register

import (
	"os"
	"time"

	wago "github.com/wago-org/wago"
	"github.com/wago-org/wasi"
	"github.com/wago-org/wasi/p1"
	"github.com/wago-org/wasi/unstable"
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
	wago.RegisterExtension("github.com/wago-org/wasi/p1", func() wago.Extension { return p1.Init(std()) })
	wago.RegisterExtension("github.com/wago-org/wasi/unstable", func() wago.Extension { return unstable.Init(std()) })
}
