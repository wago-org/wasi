//go:build linux && (amd64 || arm64)

package core

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// A sparse anonymous mapping exercises the last memory32 byte without
// allocating or touching four GiB of physical memory.
func sparseMemory32(t *testing.T) []byte {
	t.Helper()
	const size = uint64(1) << 32
	mem, err := unix.Mmap(-1, 0, int(size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE|unix.MAP_ANON|unix.MAP_NORESERVE)
	if err != nil {
		t.Skipf("reserve sparse memory32 mapping: %v", err)
	}
	t.Cleanup(func() {
		if err := unix.Munmap(mem); err != nil {
			t.Error(err)
		}
	})
	return mem
}

func boundaryCall(t *testing.T, call func()) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Errorf("valid memory32 end range panicked: %v", recovered)
		}
	}()
	call()
}

func TestMemory32EndBoundary(t *testing.T) {
	mem := sparseMemory32(t)
	const end = uint64(1) << 32
	mem[end-2], mem[end-1] = 'a', 'b'
	module := testModule{mem}
	t.Run("path bytes", func(t *testing.T) {
		boundaryCall(t, func() {
			got, code := guestBytes(mem, uint32(end-2), 2)
			if code != wasiOK || got != "ab" {
				t.Fatalf("guestBytes = %q, %d", got, code)
			}
		})
	})
	t.Run("iovec", func(t *testing.T) {
		boundaryCall(t, func() {
			binary.LittleEndian.PutUint32(mem[0:], uint32(end-2))
			binary.LittleEndian.PutUint32(mem[4:], 2)
			bufs, code := (&Plugin{cfg: Config{MaxIOVecs: 1}}).iovecs(mem, 0, 1)
			if code != wasiOK || len(bufs) != 1 || !bytes.Equal(bufs[0], []byte("ab")) {
				t.Fatalf("iovecs = %d, %v", code, bufs)
			}
		})
	})
	t.Run("filestat", func(t *testing.T) {
		boundaryCall(t, func() {
			info, err := os.Stat(os.DevNull)
			if err != nil {
				t.Fatal(err)
			}
			if code := writeFilestat(mem, uint32(end-64), info); code != wasiOK {
				t.Fatalf("writeFilestat = %d", code)
			}
		})
	})
	t.Run("prestat", func(t *testing.T) {
		boundaryCall(t, func() {
			e := &Plugin{fs: &fsState{fds: map[uint32]*fdEntry{3: {preopen: "/"}}}}
			var result [1]uint64
			e.fdPrestatGet(module, []uint64{3, end - 8}, result[:])
			if result[0] != wasiOK {
				t.Fatalf("fdPrestatGet = %d", result[0])
			}
		})
	})
	t.Run("random", func(t *testing.T) {
		boundaryCall(t, func() {
			e := &Plugin{cfg: Config{Rand: bytes.NewReader([]byte("xy"))}}
			var result [1]uint64
			e.randomGet(module, []uint64{end - 2, 2}, result[:])
			if result[0] != wasiOK || string(mem[end-2:end]) != "xy" {
				t.Fatalf("randomGet = %d, %q", result[0], mem[end-2:end])
			}
		})
	})
	t.Run("poll input", func(t *testing.T) {
		boundaryCall(t, func() {
			clear(mem[end-48 : end])
			e := &Plugin{cfg: Config{Clocks: newSystemClock(nil)}}
			var result [1]uint64
			e.pollOneoff(module, []uint64{end - 48, 64, 1, 128}, result[:])
			if result[0] != wasiOK {
				t.Fatalf("pollOneoff = %d", result[0])
			}
		})
	})
	t.Run("poll output", func(t *testing.T) {
		boundaryCall(t, func() {
			clear(mem[:48])
			e := &Plugin{cfg: Config{Clocks: newSystemClock(nil)}}
			var result [1]uint64
			e.pollOneoff(module, []uint64{0, end - 32, 1, 128}, result[:])
			if result[0] != wasiOK {
				t.Fatalf("pollOneoff = %d", result[0])
			}
		})
	})
	t.Run("strings cannot wrap", func(t *testing.T) {
		boundaryCall(t, func() {
			mem[0] = 0x7b
			code := writeStrings(mem, 64, uint32(end-1), []string{"", "x"})
			if code != wasiEFault || mem[0] != 0x7b {
				t.Fatalf("writeStrings = %d, sentinel = %#x", code, mem[0])
			}
		})
	})
	t.Run("readlink", func(t *testing.T) {
		boundaryCall(t, func() {
			root := t.TempDir()
			if err := os.Symlink("ab", filepath.Join(root, "link")); err != nil {
				t.Fatal(err)
			}
			e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/", HostPath: root, Read: true}}})
			copy(mem[160:], "link")
			var result [1]uint64
			e.pathReadlink(module, []uint64{3, 160, 4, end - 2, 2, 192}, result[:])
			if result[0] != wasiOK || string(mem[end-2:end]) != "ab" {
				t.Fatalf("pathReadlink = %d, %q", result[0], mem[end-2:end])
			}
		})
	})
}

func BenchmarkGuestBytesShort(b *testing.B) {
	mem := make([]byte, 128)
	copy(mem[64:], "name")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got, code := guestBytes(mem, 64, 4); code != wasiOK || got != "name" {
			b.Fatal(got, code)
		}
	}
}
