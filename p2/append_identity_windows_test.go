//go:build windows

package p2

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
)

func TestWindowsAppendTargetsAreStripedByFile(t *testing.T) {
	root := t.TempDir()
	s := newFilesystem(nil, Limits{})
	targets := make(map[*appendTarget]bool)
	for i := 0; i < 32; i++ {
		f, err := os.OpenFile(filepath.Join(root, fmt.Sprintf("file-%02d", i)), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		node := &descriptorNode{file: f}
		if _, err := s.addDesc(node); err != nil {
			t.Fatal(err)
		}
		targets[node.append] = true
	}
	if len(targets) < 2 {
		t.Fatalf("32 unrelated files use %d append lock; want multiple stripes", len(targets))
	}
}

// Measure the actual append hot path with one independently opened file per
// parallel worker. Descriptor registration happens before the measured writes.
func BenchmarkWindowsAppendIndependentFiles(b *testing.B) {
	root := b.TempDir()
	streams := make([]*fileStream, runtime.GOMAXPROCS(0))
	for i := range streams {
		f, err := os.OpenFile(filepath.Join(root, fmt.Sprintf("file-%d", i)), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			b.Fatal(err)
		}
		defer f.Close()
		s := newFilesystem(nil, Limits{})
		node := &descriptorNode{file: f}
		if _, err := s.addDesc(node); err != nil {
			b.Fatal(err)
		}
		streams[i] = &fileStream{file: f, append: node.append}
	}
	data := []byte("append\n")
	var next atomic.Uint32
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		stream := streams[next.Add(1)-1]
		for pb.Next() {
			if n, err := stream.Write(data); err != nil || n != len(data) {
				b.Errorf("append = %d, %v", n, err)
				return
			}
		}
	})
}

func BenchmarkWindowsAppendTargetIdentity(b *testing.B) {
	f, err := os.Create(filepath.Join(b.TempDir(), "file"))
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := appendTargetForFile(f, info); err != nil {
			b.Fatal(err)
		}
	}
}
