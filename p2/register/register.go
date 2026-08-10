// Package register wires the WASI Preview 2 consumer into Wago's global plugin
// registry as a side effect of import. Importing this package also registers the
// standalone Component Model provider required by Preview 2.
package register

import (
	"os"
	"time"

	_ "github.com/wago-org/component-model/register"
	"github.com/wago-org/wago"
	"github.com/wago-org/wasi/p2"
)

func init() {
	wago.RegisterExtension(p2.ID, func() wago.Extension {
		return p2.NewExtension(p2.Config{
			Stdout:    os.Stdout,
			Stderr:    os.Stderr,
			Stdin:     os.Stdin,
			Env:       os.Environ(),
			Args:      wago.GuestArgs(),
			WallClock: time.Now,
		})
	})
}
