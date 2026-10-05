package p2

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestStatUnderPathFlagsUsesSymlinkFlag(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "directory", "file"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := openPreopenDirectory(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	for link, target := range map[string]string{"filelink": "directory/file", "dirlink": "directory"} {
		if err := hostFS.Symlinkat(target, int(base.Fd()), link); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, path, want string
		flags            uint32
	}{
		{"link itself", "filelink", "filelink", 0},
		{"link target", "filelink", "directory/file", 1},
		{"intermediate link", "dirlink/file", "directory/file", 0},
		{"directory link with slash", "dirlink/", "directory", 0},
		{"parent after intermediate link", "dirlink/../directory/file", "directory/file", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := statUnderPathFlags(base, tc.path, tc.flags)
			if err != nil {
				t.Fatalf("metadata lookup %q with flags %d: %v", tc.path, tc.flags, err)
			}
			want, err := os.Lstat(filepath.Join(root, filepath.FromSlash(tc.want)))
			if err != nil {
				t.Fatal(err)
			}
			if got.Mode().Type() != want.Mode().Type() || got.Size() != want.Size() {
				t.Fatalf("metadata = %v, %d; want %v, %d", got.Mode(), got.Size(), want.Mode(), want.Size())
			}
			if !reflect.DeepEqual(metadataHash(got), metadataHash(want)) {
				t.Fatalf("metadata hash = %v, want %v", metadataHash(got), metadataHash(want))
			}
		})
	}
}

func BenchmarkStatUnderPathFlags(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("data"), 0o600); err != nil {
		b.Fatal(err)
	}
	base, err := openPreopenDirectory(root, false)
	if err != nil {
		b.Fatal(err)
	}
	defer base.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := statUnderPathFlags(base, "file", 0); err != nil {
			b.Fatal(err)
		}
	}
}
