//go:build linux

package core

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPathWalkFallbackPreservesComponentChecks(t *testing.T) {
	root, e := pathLookupFixture(t)
	if err := os.Symlink("sub/deeper", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		code uint64
	}{
		{"missing/../file", wasiENoent},
		{"file/../file", wasiENotdir},
		{"sub/../file", wasiOK},
		{"link/../file", wasiENotdir},
		{"sub/../../file", wasiENotcapable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, code := openAtWalk(e.fs.fds[3], tc.name, hostOpenReadOnly, 0)
			if file != nil {
				defer file.Close()
			}
			if code != tc.code {
				t.Fatalf("fallback %q: errno %d, want %d", tc.name, code, tc.code)
			}
			if code == wasiOK {
				content, err := io.ReadAll(file)
				if err != nil || string(content) != "root" {
					t.Fatalf("fallback content %q, error %v", content, err)
				}
			}
		})
	}
}

func BenchmarkPathWalkFallback(b *testing.B) {
	root := b.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub", "deeper"), 0o700); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0o600); err != nil {
		b.Fatal(err)
	}
	file, err := os.Open(root)
	if err != nil {
		b.Fatal(err)
	}
	defer file.Close()
	d := &fdEntry{file: file}
	for _, name := range []string{"file", "sub/deeper/../../file"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				f, code := openAtWalk(d, name, hostOpenReadOnly, 0)
				if code != wasiOK {
					b.Fatal(code)
				}
				f.Close()
			}
		})
	}
}
