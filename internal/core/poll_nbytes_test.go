package core

import (
	"encoding/binary"
	"os"
	"testing"
)

func TestPollReadEventReportsRemainingRegularFileBytes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(root+"/file", []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/", HostPath: root, Read: true}}})
	mem := make([]byte, 256)
	copy(mem[32:], "file")
	result := make([]uint64, 1)
	e.pathOpen(testModule{mem}, []uint64{3, 0, 32, 4, 0, rightFDRead | rightFDSeek | rightPollFDReadWrite, 0, 0, 64}, result)
	if result[0] != wasiOK {
		t.Fatalf("path_open errno = %d", result[0])
	}
	fd := binary.LittleEndian.Uint32(mem[64:])
	defer e.fdClose(testModule{mem}, []uint64{uint64(fd)}, result)

	e.fdSeek(testModule{mem}, []uint64{uint64(fd), 2, 0, 72}, result)
	if result[0] != wasiOK {
		t.Fatalf("fd_seek errno = %d", result[0])
	}
	putFDSubscription(mem, 0, 7, 1, fd)
	e.pollOneoff(testModule{mem}, []uint64{0, 128, 1, 120}, result)
	if result[0] != wasiOK || binary.LittleEndian.Uint32(mem[120:]) != 1 {
		t.Fatalf("poll_oneoff = (%d, %d events), want (OK, 1)", result[0], binary.LittleEndian.Uint32(mem[120:]))
	}
	if got := binary.LittleEndian.Uint64(mem[128+16:]); got != 3 {
		t.Fatalf("read event nbytes = %d, want 3 remaining bytes", got)
	}
}
