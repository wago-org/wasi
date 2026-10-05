//go:build windows

package p2

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsAppendUnsupportedIdentityUsesVolume(t *testing.T) {
	var want *appendTarget
	for _, unsupported := range []error{windows.ERROR_INVALID_PARAMETER, windows.ERROR_NOT_SUPPORTED, windows.ERROR_INVALID_FUNCTION, windows.ERROR_INVALID_LEVEL} {
		t.Run(unsupported.Error(), func(t *testing.T) {
			for _, handle := range []windows.Handle{101, 202} {
				idCalls, volumeCalls := 0, 0
				target, err := appendTargetForWindowsHandle(handle, func(h windows.Handle) (windowsAppendID, error) {
					idCalls++
					if h != handle {
						t.Fatal("identity query received another handle")
					}
					return windowsAppendID{}, unsupported
				}, func(h windows.Handle) (uint32, error) {
					volumeCalls++
					if h != handle {
						t.Fatal("volume query received another handle")
					}
					return 0x12345678, nil
				})
				if err != nil || target == nil || idCalls != 1 || volumeCalls != 1 {
					t.Fatalf("unsupported identity = %p, %v; queries %d/%d", target, err, idCalls, volumeCalls)
				}
				if want == nil {
					want = target
				} else if target != want {
					t.Fatal("fallback aliases on the same volume must share one lock")
				}
			}
		})
	}
}

func TestWindowsAppendQueryErrorsCloseNewFile(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "identity", true: "volume"}[fallback], func(t *testing.T) {
			f, err := os.Create(filepath.Join(t.TempDir(), "file"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			info, err := f.Stat()
			if err != nil {
				t.Fatal(err)
			}
			volumeCalls := 0
			target, err := appendTargetForNewFile(f, info, func(file *os.File, _ fs.FileInfo) (*appendTarget, error) {
				return appendTargetForWindowsHandle(windows.Handle(file.Fd()), func(windows.Handle) (windowsAppendID, error) {
					if fallback {
						return windowsAppendID{}, windows.ERROR_INVALID_LEVEL
					}
					return windowsAppendID{}, windows.ERROR_ACCESS_DENIED
				}, func(windows.Handle) (uint32, error) {
					volumeCalls++
					return 0, windows.ERROR_ACCESS_DENIED
				})
			})
			if target != nil || !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				t.Fatalf("unexpected driver error = %p, %v", target, err)
			}
			if _, err := f.Stat(); !errors.Is(err, os.ErrClosed) && !errors.Is(err, windows.ERROR_INVALID_HANDLE) {
				t.Fatalf("new file after identity failure = %v, want closed", err)
			}
			wantCalls := 0
			if fallback {
				wantCalls = 1
			}
			if volumeCalls != wantCalls {
				t.Fatalf("volume queries = %d, want %d", volumeCalls, wantCalls)
			}
		})
	}
}
