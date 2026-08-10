// Package register wires the explicit WASI Preview 1 plugin into Wago's global
// plugin registry as a side effect of import.
package register

import (
	"os"
	"time"

	"github.com/wago-org/wago"
	"github.com/wago-org/wasi"
	"github.com/wago-org/wasi/p1"
)

func init() {
	wago.RegisterExtension(p1.ID, func() wago.Extension {
		return p1.Init(wasi.Config{
			Stdout: os.Stdout,
			Stderr: os.Stderr,
			Stdin:  os.Stdin,
			Env:    os.Environ(),
			Now:    func() int64 { return time.Now().UnixNano() },
		})
	})
}
