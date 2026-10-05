package core

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"testing"
)

func TestDirectoryReadErrorsAreNotEndOfDirectory(t *testing.T) {
	for _, operation := range []string{"enumerate", "advance-cookie"} {
		t.Run(operation, func(t *testing.T) {
			e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: t.TempDir(), Read: true}}})
			defer e.closeAll()
			m := testModule{mem: make([]byte, 256)}
			result := []uint64{0}
			e.fdReaddir(m, []uint64{3, 32, 64, 0, 112}, result)
			if result[0] != wasiOK {
				t.Fatalf("initial readdir=%d", result[0])
			}
			entry := e.fs.fds[3]
			if err := entry.dirIter.Close(); err != nil {
				t.Fatal(err)
			}
			entries, hostErr := entry.dirIter.ReadDir(1)
			if len(entries) != 0 || hostErr == nil || errors.Is(hostErr, io.EOF) {
				t.Fatalf("closed directory read=%v,%v; require non-EOF error with zero entries", entries, hostErr)
			}
			var code uint64
			if operation == "enumerate" {
				e.fdReaddir(m, []uint64{3, 32, 64, 2, 112}, result)
				code = result[0]
			} else {
				code = skipDirectoryEntries(entry, 1)
			}
			if want := errno(hostErr); code != want {
				t.Fatalf("%s error=%d, want host error %d (%v)", operation, code, want, hostErr)
			}
		})
	}
}

func TestDirectoryReadEOFPreservesEndOfDirectory(t *testing.T) {
	e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: t.TempDir(), Read: true}}})
	defer e.closeAll()
	m := testModule{mem: make([]byte, 256)}
	result := []uint64{0}
	e.fdReaddir(m, []uint64{3, 32, 64, 0, 112}, result)
	if result[0] != wasiOK {
		t.Fatalf("initial readdir=%d", result[0])
	}
	entry := e.fs.fds[3]
	entries, hostErr := entry.dirIter.ReadDir(1)
	if len(entries) != 0 || !errors.Is(hostErr, io.EOF) {
		t.Fatalf("empty directory read=%v,%v; require EOF", entries, hostErr)
	}
	e.fdReaddir(m, []uint64{3, 32, 64, 2, 112}, result)
	if result[0] != wasiOK || binary.LittleEndian.Uint32(m.mem[112:]) != 0 {
		t.Fatalf("EOF readdir=%d, used=%d", result[0], binary.LittleEndian.Uint32(m.mem[112:]))
	}
	if code := skipDirectoryEntries(entry, 1); code != wasiOK {
		t.Fatalf("EOF cookie advance=%d", code)
	}
}

func BenchmarkDirectoryCookieAdvanceEOF(b *testing.B) {
	dir, err := os.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer dir.Close()
	entry := fdEntry{dirIter: dir}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if code := skipDirectoryEntries(&entry, 1); code != wasiOK {
			b.Fatalf("cookie advance=%d", code)
		}
	}
}
