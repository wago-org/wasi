//go:build windows

package core

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestErrnoMapsUnsupportedWindowsErrors(t *testing.T) {
	if got := errno(windows.ERROR_NOT_SUPPORTED); got != wasiENotsup {
		t.Errorf("errno(ERROR_NOT_SUPPORTED) = %d, want %d", got, wasiENotsup)
	}
	if got := errno(windows.ERROR_CALL_NOT_IMPLEMENTED); got != wasiENosys {
		t.Errorf("errno(ERROR_CALL_NOT_IMPLEMENTED) = %d, want %d", got, wasiENosys)
	}
}
