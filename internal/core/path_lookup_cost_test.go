//go:build darwin || windows

package core

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPathLookupParentStepsScaleLinearly(t *testing.T) {
	_, e := pathLookupFixture(t)
	d := e.fs.fds[3]
	measure := func(steps int) testing.BenchmarkResult {
		t.Helper()
		name := strings.Repeat("sub/../", steps) + "file"
		return testing.Benchmark(func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				file, code := openAt(d, name, hostOpenReadOnly, 0)
				if code != wasiOK {
					b.Fatalf("open after %d parent steps: errno %d", steps, code)
				}
				_ = file.Close()
			}
		})
	}
	short, long := measure(12), measure(48)
	// Four times as many components may require four times as many opens.
	// Rebuilding the remaining suffix and replaying from root on each ".."
	// makes the allocated bytes grow quadratically.
	if long.AllocedBytesPerOp() > short.AllocedBytesPerOp()*6 {
		t.Fatalf("parent-step bytes grew superlinearly: 12 steps %d B/op, 48 steps %d B/op", short.AllocedBytesPerOp(), long.AllocedBytesPerOp())
	}
	name := strings.Repeat("sub/../", 48) + ".."
	if file, code := openAt(d, name, hostOpenReadOnly, 0); code != wasiENotcapable {
		if file != nil {
			_ = file.Close()
		}
		t.Fatalf("parent step beyond preopen: errno %d, want %d", code, wasiENotcapable)
	}
}

func TestPathLookupPinnedDepthBound(t *testing.T) {
	root := t.TempDir()
	deep := strings.Repeat("d/", maxPinnedPathDepth+1)
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(deep)), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := openPreopen(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	hostRoot, err := hostMountRoot(f, root)
	if err != nil {
		t.Fatal(err)
	}
	d := &fdEntry{file: f, root: hostRoot}
	for _, tc := range []struct {
		depth int
		code  uint64
	}{{maxPinnedPathDepth, wasiOK}, {maxPinnedPathDepth + 1, wasiENametoolong}} {
		name := strings.Repeat("d/", tc.depth) + ".."
		file, code := openAt(d, name, hostOpenReadOnly, 0)
		if file != nil {
			_ = file.Close()
		}
		if code != tc.code {
			t.Fatalf("depth %d: errno %d, want %d", tc.depth, code, tc.code)
		}
	}
}

func BenchmarkPathLookupParentSteps(b *testing.B) {
	root := b.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o700); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0o600); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "file"), nil, 0o600); err != nil {
		b.Fatal(err)
	}
	f, err := openPreopen(root, false)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	hostRoot, err := hostMountRoot(f, root)
	if err != nil {
		b.Fatal(err)
	}
	d := &fdEntry{file: f, root: hostRoot}
	cases := []struct{ label, name string }{
		{"file", "file"},
		{"sub-file", "sub/file"},
		{"parent-1", "sub/../file"},
	}
	for _, steps := range []int{16, 64, 256} {
		cases = append(cases, struct{ label, name string }{
			label: "parent-" + strconv.Itoa(steps), name: strings.Repeat("sub/../", steps) + "file",
		})
	}
	for _, tc := range cases {
		b.Run(tc.label, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				file, code := openAt(d, tc.name, hostOpenReadOnly, 0)
				if code != wasiOK {
					b.Fatalf("open: errno %d", code)
				}
				_ = file.Close()
			}
		})
	}
}
