//go:build linux

package core

import (
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestFDPwriteReportsPartialWrite(t *testing.T) {
	const child = "WASI_PWRITE_PARTIAL_CHILD"
	if os.Getenv(child) != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+regexp.QuoteMeta(t.Name())+"$")
		cmd.Env = append(os.Environ(), child+"=1")
		output, err := cmd.CombinedOutput()
		if ctx.Err() != nil {
			t.Fatalf("partial write child timed out: %s", output)
		}
		if err != nil {
			t.Fatalf("partial write child: %v\n%s", err, output)
		}
		return
	}

	f, err := os.CreateTemp(t.TempDir(), "limited")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	signal.Ignore(syscall.SIGXFSZ)
	if err := unix.Setrlimit(unix.RLIMIT_FSIZE, &unix.Rlimit{Cur: 5, Max: 5}); err != nil {
		t.Fatal(err)
	}
	e := newTestPlugin(t, Config{})
	e.fs.fds[3] = &fdEntry{file: f, rights: rightFDWrite | rightFDSeek}
	mem := make([]byte, 64)
	binary.LittleEndian.PutUint32(mem[0:], 16)
	binary.LittleEndian.PutUint32(mem[4:], 10)
	copy(mem[16:], "abcdefghij")
	binary.LittleEndian.PutUint32(mem[32:], 99)
	r := make([]uint64, 1)
	e.fdPwrite(testModule{mem}, []uint64{3, 0, 1, 0, 32}, r)
	data, err := os.ReadFile(f.Name())
	if err != nil || string(data) != "abcde" {
		t.Fatalf("host file = %q, %v; want first five bytes", data, err)
	}
	if r[0] != wasiOK || binary.LittleEndian.Uint32(mem[32:]) != 5 {
		t.Fatalf("fd_pwrite = errno %d, bytes %d after writing %q; want success and 5 bytes", r[0], binary.LittleEndian.Uint32(mem[32:]), data)
	}
}
