//go:build linux || darwin

package core

import (
	"syscall"
	"testing"
)

func TestErrnoMapsUnsupportedHostErrors(t *testing.T) {
	if got := errno(syscall.ENOTSUP); got != wasiENotsup {
		t.Errorf("errno(ENOTSUP) = %d, want %d", got, wasiENotsup)
	}
	const wasiENosys = 52
	if got := errno(syscall.ENOSYS); got != wasiENosys {
		t.Errorf("errno(ENOSYS) = %d, want %d", got, wasiENosys)
	}
}
