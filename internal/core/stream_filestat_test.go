package core

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"testing"
)

func TestStreamFilestat(t *testing.T) {
	for _, fd := range []uint64{0, 1, 2} {
		t.Run(fmt.Sprintf("fd%d", fd), func(t *testing.T) {
			e := newTestPlugin(t, Config{})
			mem := bytes.Repeat([]byte{0xa5}, 128)
			result := []uint64{99}
			e.fdFilestatGet(testModule{mem}, []uint64{fd, 32}, result)
			if result[0] != wasiOK {
				t.Fatalf("fd_filestat_get(%d) = %d, want success", fd, result[0])
			}
			want := make([]byte, 64)
			want[16] = filetypeCharacterDevice
			binary.LittleEndian.PutUint64(want[24:], 1)
			if !bytes.Equal(mem[32:96], want) {
				t.Fatalf("stream filestat = %x, want %x", mem[32:96], want)
			}
			if !bytes.Equal(mem[:32], bytes.Repeat([]byte{0xa5}, 32)) || !bytes.Equal(mem[96:], bytes.Repeat([]byte{0xa5}, 32)) {
				t.Fatal("filestat modified bytes outside its result")
			}
			e.fdFdstatGet(testModule{mem}, []uint64{fd, 0}, result)
			if result[0] != wasiOK || mem[0] != mem[48] {
				t.Fatal("fdstat and filestat disagree on stream type")
			}
		})
	}
}

func TestStreamFilestatBoundsAndRights(t *testing.T) {
	for _, ptr := range []uint64{65, 128, 0xffffffff} {
		t.Run(fmt.Sprint(ptr), func(t *testing.T) {
			e := newTestPlugin(t, Config{})
			mem := bytes.Repeat([]byte{0xa5}, 128)
			before := append([]byte(nil), mem...)
			result := []uint64{99}
			e.fdFilestatGet(testModule{mem}, []uint64{0, ptr}, result)
			if result[0] != wasiEFault || !bytes.Equal(mem, before) {
				t.Fatalf("out-of-bounds filestat = %d or modified memory", result[0])
			}
		})
	}
	e := newTestPlugin(t, Config{})
	mem := bytes.Repeat([]byte{0xa5}, 128)
	result := []uint64{99}
	// The exact end of memory is a valid result buffer.
	e.fdFilestatGet(testModule{mem}, []uint64{0, 64}, result)
	if result[0] != wasiOK {
		t.Fatalf("end-of-memory result = %d", result[0])
	}
	before := append([]byte(nil), mem...)
	e.fdFilestatGet(testModule{mem}, []uint64{99, 0xffffffff}, result)
	if result[0] != wasiEBadf {
		t.Fatalf("invalid descriptor = %d", result[0])
	}
	e.fdFdstatSetRights(testModule{mem}, []uint64{1, rightFDWrite, 0}, result)
	if result[0] != wasiOK {
		t.Fatal(result[0])
	}
	e.fdFilestatGet(testModule{mem}, []uint64{1, 0xffffffff}, result)
	if result[0] != wasiENotcapable {
		t.Fatalf("attenuated descriptor = %d", result[0])
	}
	e.fdClose(testModule{mem}, []uint64{2}, result)
	if result[0] != wasiOK {
		t.Fatal(result[0])
	}
	e.fdFilestatGet(testModule{mem}, []uint64{2, 0xffffffff}, result)
	if result[0] != wasiEBadf {
		t.Fatalf("closed descriptor = %d", result[0])
	}
	if !bytes.Equal(mem, before) {
		t.Fatal("rejected filestat modified memory")
	}
}

func TestStreamFilestatRenumberAndOwnership(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "caller-owned")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString("caller-owned metadata"); err != nil {
		t.Fatal(err)
	}
	e := newTestPlugin(t, Config{Stdin: file, Stdout: file, Stderr: file, Mounts: []Preopen{{GuestPath: "/", HostPath: t.TempDir(), Read: true}}})
	m := testModule{make([]byte, 128)}
	result := []uint64{99}
	e.fdRenumber(m, []uint64{0, 3}, result)
	if result[0] != wasiOK {
		t.Fatal(result[0])
	}
	for _, fd := range []uint64{1, 2, 3} {
		e.fdFilestatGet(m, []uint64{fd, 0}, result)
		if result[0] != wasiOK || m.mem[16] != filetypeCharacterDevice || binary.LittleEndian.Uint64(m.mem[32:]) != 0 {
			t.Fatalf("caller-owned stream %d = errno %d metadata %x", fd, result[0], m.mem[:64])
		}
		e.fdClose(m, []uint64{fd}, result)
		if result[0] != wasiOK {
			t.Fatal(result[0])
		}
	}
	e.fdFilestatGet(m, []uint64{0, 0}, result)
	if result[0] != wasiEBadf {
		t.Fatalf("renumber source = %d", result[0])
	}
	if _, err := file.WriteString("still open"); err != nil {
		t.Fatalf("guest closed caller-owned stream: %v", err)
	}
}

func BenchmarkFDFileStatGet(b *testing.B) {
	for _, stream := range []bool{true, false} {
		name := "file"
		if stream {
			name = "stream"
		}
		b.Run(name, func(b *testing.B) {
			e := &Plugin{module: "wasi_snapshot_preview1", cfg: cloneConfig(Config{})}
			e.resetFS()
			if err := e.initFS(false); err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { closeFS(e.fs) })
			fd := uint64(0)
			if !stream {
				f, err := os.CreateTemp(b.TempDir(), "filestat")
				if err != nil {
					b.Fatal(err)
				}
				e.fs.fds[3] = &fdEntry{file: f, rights: rightFDFilestatGet}
				fd = 3
			}
			m := testModule{make([]byte, 64)}
			args := []uint64{fd, 0}
			result := []uint64{0}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				e.fdFilestatGet(m, args, result)
			}
		})
	}
}
