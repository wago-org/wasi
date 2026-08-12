// Package register exposes WASI's explicit provider catalog to generated Wago
// runtimes. Importing this package has no registration side effects.
package register

import (
	wago "github.com/wago-org/wago"
	"github.com/wago-org/wasi"
	"github.com/wago-org/wasi/p1"
	"github.com/wago-org/wasi/unstable"
)

// Providers returns a fresh catalog. The root and p1 entries intentionally
// target the same Wasm module, so a reviewed plugin set selects one, never both.
func Providers() []wago.PluginProvider {
	return []wago.PluginProvider{
		wasi.Provider(),
		p1.Provider(),
		unstable.Provider(),
	}
}
