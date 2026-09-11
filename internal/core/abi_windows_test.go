//go:build windows

package core

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsErrnoMapping(t *testing.T) {
	tests := map[error]uint64{
		windows.ERROR_ACCESS_DENIED:        wasiEAcces,
		windows.ERROR_DIR_NOT_EMPTY:        wasiENotempty,
		windows.ERROR_DISK_FULL:            wasiENospc,
		windows.ERROR_NOT_SAME_DEVICE:      wasiEXdev,
		windows.ERROR_SHARING_VIOLATION:    wasiEBusy,
		windows.ERROR_FILENAME_EXCED_RANGE: wasiENametoolong,
	}
	for input, want := range tests {
		if got := errno(input); got != want {
			t.Errorf("errno(%v) = %d, want %d", input, got, want)
		}
	}
}
