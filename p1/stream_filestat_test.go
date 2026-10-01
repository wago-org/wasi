//go:build linux && amd64 && !tinygo

package p1_test

import (
	"encoding/binary"
	"testing"

	"github.com/wago-org/wasi/p1"
)

// CPython queries filestat while creating sys.stdin, sys.stdout and sys.stderr.
func TestPreview1StreamFilestat(t *testing.T) {
	h := newPreview1Harness(t, p1.Config{},
		preview1Func{"fd_filestat_get", []byte{wasmI32, wasmI32}},
		preview1Func{"fd_fdstat_get", []byte{wasmI32, wasmI32}},
	)
	for fd := uint64(0); fd < 3; fd++ {
		requireErrno(t, errnoSuccess, h.call(t, "fd_filestat_get", fd, 64))
		requireErrno(t, errnoSuccess, h.call(t, "fd_fdstat_get", fd, 0))
		if h.memory()[80] != 2 || h.memory()[0] != h.memory()[80] || binary.LittleEndian.Uint64(h.memory()[88:]) != 1 {
			t.Fatalf("stdio %d has inconsistent character-device metadata", fd)
		}
	}
	requireErrno(t, errnoFault, h.call(t, "fd_filestat_get", 0, 65500))
	requireErrno(t, errnoBadf, h.call(t, "fd_filestat_get", 99, 64))
}
