// Package register wires the legacy WASI unstable plugin into Wago's global
// plugin registry as a side effect of import.
package register

import (
	"os"
	"time"

	"github.com/wago-org/wago"
	"github.com/wago-org/wasi"
	"github.com/wago-org/wasi/unstable"
)

func init() {
	wago.RegisterExtension(unstable.ID, func() wago.Extension {
		return unstable.Init(wasi.Config{
			Stdout: os.Stdout,
			Stderr: os.Stderr,
			Stdin:  os.Stdin,
			Env:    os.Environ(),
			Now:    func() int64 { return time.Now().UnixNano() },
		})
	})
}
