package core

import (
	"os"
	"path"
	"strings"
	"testing"
)

func splitPathEscapes(guest string) bool {
	depth := 0
	for _, part := range strings.Split(guest, "/") {
		switch part {
		case "", ".":
		case "..":
			depth--
		default:
			depth++
		}
		if depth < 0 {
			return true
		}
	}
	return false
}

func TestPathComponentScan(t *testing.T) {
	for _, s := range []string{"a/b/c", "a//b", "./a", "a/../b", "../a", "a/../../b", strings.Repeat("./", 4096) + "a", strings.Repeat("a/", 4096), "a/", "", "/", "../a/..", "a/../../../a"} {
		if got, want := pathEscapes(s), splitPathEscapes(s); got != want {
			t.Fatalf("path %q: %v != %v", s, got, want)
		}
	}
}

func TestPathComponentScanAllocations(t *testing.T) {
	for _, s := range []string{"a/b/c", strings.Repeat("a/", 4096), strings.Repeat("./", 4096) + "a"} {
		if n := testing.AllocsPerRun(100, func() { pathScanSink = pathEscapes(s) }); n != 0 {
			t.Fatalf("depth scan allocates %v", n)
		}
	}
}

func FuzzPathComponentScan(f *testing.F) {
	for _, s := range []string{"", "a//b/../", "../a", "a/../../b", "./a", "a/", "/a", "a\x00/b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if got, want := pathEscapes(s), splitPathEscapes(s); got != want {
			t.Fatalf("%q: %v != %v", s, got, want)
		}
	})
}

func TestPathResolveOrdering(t *testing.T) {
	dir, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	e := &Plugin{fs: &fsState{fds: map[uint32]*fdEntry{3: {file: dir}, 0: {}}}}
	for _, s := range []string{"a/b/c", "a//b", "./a", "a/../b", "../a", "a/../../b", strings.Repeat("./", 4096) + "a", strings.Repeat("a/", 4096), "a/", "", "/a"} {
		_, clean, code := e.resolve(3, s)
		want := uint64(wasiOK)
		if s == "" || strings.HasPrefix(s, "/") || splitPathEscapes(s) {
			want = wasiENotcapable
		}
		if code != want || code == wasiOK && clean != path.Clean(s) {
			t.Fatalf("%q: %q %d", s, clean, code)
		}
		if _, _, code := e.resolve(99, s); code != wasiEBadf {
			t.Fatalf("fd error order: %d", code)
		}
		if _, _, code := e.resolve(0, s); code != wasiENotdir {
			t.Fatalf("directory error order: %d", code)
		}
	}
	for _, p := range [][2]uint32{{8, 1}, {1, ^uint32(0)}, {^uint32(0), 2}} {
		if _, code := guestBytes(make([]byte, 8), p[0], p[1]); code != wasiEFault {
			t.Fatal("invalid guest range accepted")
		}
	}
}

var pathScanSink bool
var pathNameSink string

func BenchmarkPathComponents(b *testing.B) {
	cases := []struct{ name, path string }{{"short", "a/b/c"}, {"many", strings.Repeat("a/", 1024) + "b"}, {"clean", strings.Repeat("./", 1024) + "b"}}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				pathScanSink = pathEscapes(tc.path)
			}
		})
	}
}
func BenchmarkPathResolve(b *testing.B) {
	dir, err := os.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer dir.Close()
	e := &Plugin{fs: &fsState{fds: map[uint32]*fdEntry{3: {file: dir}}}}
	cases := []struct{ name, path string }{{"short", "a/b/c"}, {"many", strings.Repeat("a/", 1024) + "b"}, {"clean", strings.Repeat("./", 1024) + "b"}}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, name, code := e.resolve(3, tc.path)
				if code != 0 {
					b.Fatal(code)
				}
				pathNameSink = name
			}
		})
	}
}
