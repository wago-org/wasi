package p2

import sys "github.com/wago-org/wasi/internal/p2sys"

// FSConfig is the capability-only filesystem configuration consumed by WASI
// Preview 2. Implementations expose only explicitly preopened filesystems; a
// nil value grants no filesystem authority.
type FSConfig interface {
	Preopens() ([]sys.FS, []string)
}
