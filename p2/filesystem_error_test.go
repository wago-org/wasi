package p2

import (
	"fmt"
	"io/fs"
	"os"
	"testing"
)

func TestFilesystemPermissionErrorCodes(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want uint32
	}{
		{"EPERM", hostFS.EPERM, fsErrNotPermitted},
		{"wrapped EPERM", fmt.Errorf("operation: %w", hostFS.EPERM), fsErrNotPermitted},
		{"path EPERM", &os.PathError{Op: "readlink", Path: "link", Err: hostFS.EPERM}, fsErrNotPermitted},
		{"EACCES", hostFS.EACCES, fsErrAccess},
		{"wrapped EACCES", fmt.Errorf("operation: %w", hostFS.EACCES), fsErrAccess},
		{"permission", fs.ErrPermission, fsErrAccess},
		{"wrapped permission", fmt.Errorf("operation: %w", fs.ErrPermission), fsErrAccess},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := fsError(test.err); got != test.want {
				t.Fatalf("fsError(%v) = %d, want %d", test.err, got, test.want)
			}
		})
	}
}

var benchmarkFilesystemErrorCode uint32

func BenchmarkFilesystemPermissionErrorCodes(b *testing.B) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{"EPERM", hostFS.EPERM},
		{"wrapped EPERM", fmt.Errorf("operation: %w", hostFS.EPERM)},
		{"EACCES", hostFS.EACCES},
		{"permission", fs.ErrPermission},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchmarkFilesystemErrorCode = fsError(test.err)
			}
		})
	}
}
