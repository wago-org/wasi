package p2

import (
	"io"
	"os"
	"testing"
)

func TestOpenAtFollowsSymlinksAccordingToPathFlags(t *testing.T) {
	testOpenAtPathFlags(t, openUnderPathFlags)
}

func TestOpenUnderWalkFollowsSymlinksAccordingToPathFlags(t *testing.T) {
	testOpenAtPathFlags(t, func(dir *os.File, name string, flags int, mode, pathFlags uint32) (*os.File, error) {
		return openUnderWalk(dir, name, flags, mode, pathFlags&1 != 0)
	})
}

func testOpenAtPathFlags(t *testing.T, open func(*os.File, string, int, uint32, uint32) (*os.File, error)) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(root+"/directory", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root+"/directory/file", []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := openPreopenDirectory(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	for link, target := range map[string]string{"filelink": "directory/file", "dirlink": "directory", "directory/backlink": "../directory/file"} {
		parent, leaf, err := parentUnder(base, link)
		if err != nil {
			t.Fatal(err)
		}
		err = hostFS.Symlinkat(target, int(parent.Fd()), leaf)
		parent.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name  string
		flags uint32
	}{
		{"filelink", 1},
		{"dirlink/file", 0},
		{"directory/backlink", 1},
		{"dirlink/../directory/file", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := open(base, tc.name, hostFS.O_RDONLY, 0, tc.flags)
			if err != nil {
				t.Fatalf("open-at %q with path flags %d: %v", tc.name, tc.flags, err)
			}
			defer f.Close()
			data, err := io.ReadAll(f)
			if err != nil || string(data) != "data" {
				t.Fatalf("opened data = %q, %v, want data", data, err)
			}
		})
	}
	if f, err := open(base, "filelink", hostFS.O_RDONLY, 0, 0); err == nil {
		f.Close()
		t.Fatal("open-at without symlink-follow opened the final symlink")
	}
	if f, err := open(base, "filelink", hostFS.O_CREAT|hostFS.O_EXCL|hostFS.O_RDWR, 0o600, 1); err == nil || fsError(err) != fsErrExist {
		if f != nil {
			f.Close()
		}
		t.Fatalf("exclusive open-at of existing symlink = %v, want exist", err)
	}
	if f, err := open(base, "directory/file/", hostFS.O_RDONLY, 0, 1); err == nil || fsError(err) != fsErrNotDirectory {
		if f != nil {
			f.Close()
		}
		t.Fatalf("open-at regular file with trailing slash = %v, want not-directory", err)
	}
	f, err := open(base, "dirlink/", hostFS.O_RDONLY, 0, 0)
	if err != nil {
		t.Fatalf("open-at directory symlink with trailing slash: %v", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.IsDir() {
		t.Fatalf("opened directory symlink = %v, %v", info, err)
	}
}

func BenchmarkOpenUnderPathFlags(b *testing.B) {
	benchmarkOpenUnderPathFlags(b, openUnderPathFlags)
}

func BenchmarkOpenUnderWalk(b *testing.B) {
	benchmarkOpenUnderPathFlags(b, func(dir *os.File, name string, flags int, mode, pathFlags uint32) (*os.File, error) {
		return openUnderWalk(dir, name, flags, mode, pathFlags&1 != 0)
	})
}

func benchmarkOpenUnderPathFlags(b *testing.B, open func(*os.File, string, int, uint32, uint32) (*os.File, error)) {
	b.Helper()
	root := b.TempDir()
	if err := os.WriteFile(root+"/file", []byte("data"), 0o600); err != nil {
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
		f, err := open(base, "file", hostFS.O_RDONLY, 0, 1)
		if err != nil {
			b.Fatal(err)
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
