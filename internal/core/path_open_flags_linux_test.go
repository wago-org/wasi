//go:build linux

package core

import (
	"encoding/binary"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPathOpenAppliesOrRejectsDescriptorFlags(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(root+"/file", []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true}}})
	m := testModule{mem: make([]byte, 128)}
	copy(m.mem[32:], "file")
	r := make([]uint64, 1)
	e.pathOpen(m, []uint64{3, 0, 32, 4, 0, rightFDRead, 0, 4, 16}, r)
	if r[0] != wasiOK {
		t.Fatalf("path_open NONBLOCK: errno %d", r[0])
	}
	fd := binary.LittleEndian.Uint32(m.mem[16:])
	defer e.fs.fds[fd].file.Close()
	flags, err := unix.FcntlInt(e.fs.fds[fd].file.Fd(), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.O_NONBLOCK == 0 {
		t.Fatal("path_open reported NONBLOCK but host descriptor is blocking")
	}
	e.pathOpen(m, []uint64{3, 0, 32, 4, 0, rightFDRead, 0, 2, 20}, r)
	if r[0] != wasiENotsup {
		t.Fatalf("path_open DSYNC errno = %d, want ENOTSUP", r[0])
	}
}
