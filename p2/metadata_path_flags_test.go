package p2

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestStatUnderPathFlagsUsesSymlinkFlag(t *testing.T) {
	testMetadataPathFlags(t, statUnderPathFlags)
}

func TestMetadataUnderWalkUsesSymlinkFlag(t *testing.T) {
	testMetadataPathFlags(t, func(dir *os.File, name string, flags uint32) (os.FileInfo, error) {
		result, err := walkUnder(dir, name, 0, 0, flags&1 != 0, statMetadataLeaf)
		return result.info, err
	})
}

func testMetadataPathFlags(t *testing.T, stat func(*os.File, string, uint32) (os.FileInfo, error)) {
	t.Helper()
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
		{"base directory", ".", ".", 0},
		{"link itself", "filelink", "filelink", 0},
		{"link target", "filelink", "directory/file", 1},
		{"directory link itself", "dirlink", "dirlink", 0},
		{"intermediate link", "dirlink/file", "directory/file", 0},
		{"directory link with slash", "dirlink/", "directory", 0},
		{"parent after intermediate link", "dirlink/../directory/file", "directory/file", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stat(base, tc.path, tc.flags)
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
	for _, name := range []string{"directory/file/", "filelink/"} {
		for _, flags := range []uint32{0, 1} {
			if _, err := stat(base, name, flags); err == nil || fsError(err) != fsErrNotDirectory {
				t.Fatalf("metadata %q with flags %d = %v, want not-directory", name, flags, err)
			}
		}
	}
}

func TestMetadataPathFlagsOnOrdinaryFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := openPreopenDirectory(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	for _, flags := range []uint32{0, 1} {
		info, err := statUnderPathFlags(base, "file", flags)
		if err != nil || !info.Mode().IsRegular() || info.Size() != 4 {
			t.Fatalf("ordinary file metadata with flags %d = %v, %v", flags, info, err)
		}
	}
	info, err := statUnderPathFlags(base, ".", 0)
	if err != nil || !info.IsDir() {
		t.Fatalf("base metadata = %v, %v", info, err)
	}
	if _, err := statUnderPathFlags(base, "file", 2); err == nil || fsError(err) != fsErrInvalid {
		t.Fatalf("unknown path flag = %v, want invalid", err)
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
