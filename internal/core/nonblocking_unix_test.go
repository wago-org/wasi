//go:build darwin || linux

package core

import (
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"regexp"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestPathOpenNonblockingFIFOReadReturnsAgain(t *testing.T) {
	root := nonblockingFIFOChild(t)
	if root == "" {
		return
	}
	if err := unix.Mkfifo(root+"/fifo", 0o600); err != nil {
		t.Fatal(err)
	}
	e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true}}})
	defer closeFS(e.fs)
	m := testModule{mem: make([]byte, 128)}
	copy(m.mem[32:], "fifo")
	r := make([]uint64, 1)
	e.pathOpen(m, []uint64{3, 0, 32, 4, 0, rightFDRead, 0, 4, 16}, r)
	if r[0] != wasiOK {
		t.Fatalf("path_open NONBLOCK: %d", r[0])
	}
	fd := binary.LittleEndian.Uint32(m.mem[16:])
	writer, err := unix.Open(root+"/fifo", unix.O_WRONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(writer)
	binary.LittleEndian.PutUint32(m.mem[64:], 96)
	binary.LittleEndian.PutUint32(m.mem[68:], 1)
	e.fdRead(m, []uint64{uint64(fd), 64, 1, 80}, r)
	if r[0] != wasiEAgain {
		t.Fatalf("empty NONBLOCK FIFO: %d, want EAGAIN", r[0])
	}
	if got := binary.LittleEndian.Uint32(m.mem[80:]); got != 0 {
		t.Fatalf("nread=%d, want 0", got)
	}
}

func TestPathOpenNonblockingFIFOWriteReturnsAgain(t *testing.T) {
	root := nonblockingFIFOChild(t)
	if root == "" {
		return
	}
	if err := unix.Mkfifo(root+"/fifo", 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := unix.Open(root+"/fifo", unix.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(reader)
	e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Write: true}}})
	defer closeFS(e.fs)
	m := testModule{mem: make([]byte, 128)}
	copy(m.mem[32:], "fifo")
	r := make([]uint64, 1)
	e.pathOpen(m, []uint64{3, 0, 32, 4, 0, rightFDWrite, 0, 4, 16}, r)
	if r[0] != wasiOK {
		t.Fatalf("path_open NONBLOCK: %d", r[0])
	}
	fd := binary.LittleEndian.Uint32(m.mem[16:])
	hostFD := int(e.fs.fds[fd].file.Fd())
	var data [4096]byte
	for {
		_, err = unix.Write(hostFD, data[:])
		if err == unix.EAGAIN {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	binary.LittleEndian.PutUint32(m.mem[64:], 96)
	binary.LittleEndian.PutUint32(m.mem[68:], 1)
	e.fdWrite(m, []uint64{uint64(fd), 64, 1, 80}, r)
	if r[0] != wasiEAgain {
		t.Fatalf("full NONBLOCK FIFO: %d, want EAGAIN", r[0])
	}
	if got := binary.LittleEndian.Uint32(m.mem[80:]); got != 0 {
		t.Fatalf("nwritten=%d, want 0", got)
	}
}

// Keep potentially blocking operations in a child process. Killing and reaping
// the child bounds a regression even when descriptor cleanup cannot interrupt it.
// The parent owns the temporary directory so it is also removed after a timeout.
func nonblockingFIFOChild(t *testing.T) string {
	t.Helper()
	const childEnv = "WASI_NONBLOCK_FIFO_CHILD"
	const rootEnv = "WASI_NONBLOCK_FIFO_ROOT"
	if os.Getenv(childEnv) == t.Name() {
		root := os.Getenv(rootEnv)
		if root == "" {
			t.Fatal("missing FIFO subprocess directory")
		}
		return root
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.v")
	command.Env = append(os.Environ(), childEnv+"="+t.Name(), rootEnv+"="+t.TempDir())
	command.WaitDelay = time.Second
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("NONBLOCK FIFO operation did not return within 5s:\n%s", output)
	}
	if err != nil {
		t.Fatalf("FIFO subprocess failed: %v\n%s", err, output)
	}
	return ""
}
