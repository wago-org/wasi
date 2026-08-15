// Package register exposes WASI's explicit provider catalog to generated Wago
// runtimes. Importing this package has no registration side effects.
package register

import (
	wago "github.com/wago-org/wago"
	"github.com/wago-org/wasi"
	"github.com/wago-org/wasi/p1"
	"github.com/wago-org/wasi/p2"
	"github.com/wago-org/wasi/unstable"
)

// Providers returns a fresh catalog. The root entry is a bundle that selects
// all three snapshot providers; each snapshot can also be selected directly.
func Providers() []wago.PluginProvider {
	return []wago.PluginProvider{
		wasi.Provider(),
		p1.Provider(),
		p2.Provider(),
		unstable.Provider(),
	}
}
