//go:build linux

package core

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	cases := []struct{ label, name string }{{"file", "file"}, {"parent-1", "sub/../file"}}
	for _, steps := range []int{16, 64, 256} {
		cases = append(cases, struct{ label, name string }{"parent-" + strconv.Itoa(steps), strings.Repeat("sub/../", steps) + "file"})
	}
	for _, tc := range cases {
		b.Run(tc.label, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				f, code := openAtWalk(d, tc.name, hostOpenReadOnly, 0)
				if code != wasiOK {
					b.Fatal(code)
				}
				f.Close()
			}
		})
	}
}

func TestPathWalkFallbackParentStepsScaleLinearly(t *testing.T) {
	_, e := pathLookupFixture(t)
	d := e.fs.fds[3]
	measure := func(steps int) testing.BenchmarkResult {
		name := strings.Repeat("sub/../", steps) + "file"
		return testing.Benchmark(func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				file, code := openAtWalk(d, name, hostOpenReadOnly, 0)
				if code != wasiOK {
					b.Fatalf("fallback open: errno %d", code)
				}
				_ = file.Close()
			}
		})
	}
	short, long := measure(12), measure(48)
	if long.AllocedBytesPerOp() > short.AllocedBytesPerOp()*6 {
		t.Fatalf("fallback bytes grew superlinearly: 12 steps %d B/op, 48 steps %d B/op", short.AllocedBytesPerOp(), long.AllocedBytesPerOp())
	}
}

func TestPathWalkFallbackPinnedDepthBound(t *testing.T) {
	root := t.TempDir()
	deep := strings.Repeat("d/", maxPinnedPathDepth+1)
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(deep)), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := &fdEntry{file: f}
	for _, tc := range []struct {
		depth int
		code  uint64
	}{{maxPinnedPathDepth, wasiOK}, {maxPinnedPathDepth + 1, wasiENametoolong}} {
		name := strings.Repeat("d/", tc.depth) + ".."
		file, code := openAtWalk(d, name, hostOpenReadOnly, 0)
		if file != nil {
			_ = file.Close()
		}
		if code != tc.code {
			t.Fatalf("depth %d: errno %d, want %d", tc.depth, code, tc.code)
		}
	}
	before, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	name := strings.Repeat("d/", maxPinnedPathDepth+1) + ".."
	for i := 0; i < 100; i++ {
		file, code := openAtWalk(d, name, hostOpenReadOnly, 0)
		if file != nil {
			_ = file.Close()
		}
		if code != wasiENametoolong {
			t.Fatalf("bounded walk %d: errno %d", i, code)
		}
	}
	after, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) > len(before)+2 {
		t.Fatalf("bounded walks leaked descriptors: before %d, after %d", len(before), len(after))
	}
}
