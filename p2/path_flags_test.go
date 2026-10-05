package p2

import (
	"io"
	"os"
	"testing"
)

func TestOpenAtFollowsSymlinksAccordingToPathFlags(t *testing.T) {
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := openUnderPathFlags(base, tc.name, hostFS.O_RDONLY, 0, tc.flags)
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
	if f, err := openUnderPathFlags(base, "filelink", hostFS.O_RDONLY, 0, 0); err == nil {
		f.Close()
		t.Fatal("open-at without symlink-follow opened the final symlink")
	}
	if f, err := openUnderPathFlags(base, "filelink", hostFS.O_CREAT|hostFS.O_EXCL|hostFS.O_RDWR, 0o600, 1); err == nil || fsError(err) != fsErrExist {
		if f != nil {
			f.Close()
		}
		t.Fatalf("exclusive open-at of existing symlink = %v, want exist", err)
	}
}

func BenchmarkOpenUnderPathFlags(b *testing.B) {
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
		f, err := openUnderPathFlags(base, "file", hostFS.O_RDONLY, 0, 1)
		if err != nil {
			b.Fatal(err)
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
