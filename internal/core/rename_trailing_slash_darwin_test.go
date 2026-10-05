//go:build darwin

package core

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestDarwinRenameTrailingSlashUnsupportedFlag(t *testing.T) {
	for _, tc := range []struct {
		name             string
		operation, probe error
		want             uint64
		calls            int
	}{
		{"unsupported EINVAL", unix.EINVAL, unix.EINVAL, wasiENotsup, 2},
		{"unsupported syscall", unix.ENOSYS, unix.ENOSYS, wasiENotsup, 2},
		{"unsupported operation", unix.ENOTSUP, nil, wasiENotsup, 1},
		{"supported invalid rename", unix.EINVAL, unix.ENOENT, wasiEInval, 2},
		{"regular leaf", unix.ENOTDIR, nil, wasiENotdir, 1},
		{"link refused", unix.ELOOP, nil, wasiELoop, 1},
		{"permission", unix.EACCES, nil, wasiEAcces, 1},
		{"nonempty destination", unix.EEXIST, nil, wasiENotempty, 1},
		{"success", nil, nil, wasiOK, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			invoke := func(oldFD int, oldName string, newFD int, newName string, flags uint32) error {
				calls++
				if flags != unix.RENAME_NOFOLLOW_ANY {
					t.Fatalf("unsafe rename flags = %#x", flags)
				}
				if calls == 1 {
					if oldFD != 10 || newFD != 11 || oldName != "source/" || newName != "target/" {
						t.Fatalf("mutation arguments changed")
					}
					return tc.operation
				}
				if oldFD != -1 || newFD != -1 || oldName != "" || newName != "" {
					t.Fatalf("probe could mutate a path")
				}
				return tc.probe
			}
			if got := renameTrailingAtDarwin(10, "source/", 11, "target/", invoke); got != tc.want {
				t.Fatalf("rename errno = %d, want %d", got, tc.want)
			}
			if calls != tc.calls {
				t.Fatalf("calls = %d, want %d", calls, tc.calls)
			}
		})
	}
}
