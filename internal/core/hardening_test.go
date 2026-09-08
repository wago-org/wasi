package core

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

type fixedClocks struct{ realtime, monotonic uint64 }

func (c fixedClocks) Realtime() (uint64, uint64, error)  { return c.realtime, 7, nil }
func (c fixedClocks) Monotonic() (uint64, uint64, error) { return c.monotonic, 3, nil }
func (fixedClocks) ProcessCPU() (uint64, uint64, error)  { return 0, 0, errUnsupportedClock }
func (fixedClocks) ThreadCPU() (uint64, uint64, error)   { return 0, 0, errUnsupportedClock }

func putClockSubscription(mem []byte, at uint32, userdata uint64, clockID uint32, timeout uint64, absolute bool) {
	binary.LittleEndian.PutUint64(mem[at:], userdata)
	mem[at+8] = 0
	binary.LittleEndian.PutUint32(mem[at+16:], clockID)
	binary.LittleEndian.PutUint64(mem[at+24:], timeout)
	if absolute {
		binary.LittleEndian.PutUint16(mem[at+40:], 1)
	}
}

func putFDSubscription(mem []byte, at uint32, userdata uint64, typ byte, fd uint32) {
	binary.LittleEndian.PutUint64(mem[at:], userdata)
	mem[at+8] = typ
	binary.LittleEndian.PutUint32(mem[at+16:], fd)
}

func poll(t *testing.T, e *Plugin, mem []byte, count uint32) (uint64, uint32) {
	t.Helper()
	result := []uint64{99}
	e.pollOneoff(testModule{mem: mem}, []uint64{0, 256, uint64(count), 240}, result)
	return result[0], binary.LittleEndian.Uint32(mem[240:])
}

func TestPollClockOrderingAndClockIdentity(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		e := newTestPlugin(t, Config{Clocks: fixedClocks{realtime: 1_000, monotonic: 500}})
		mem := make([]byte, 512)
		if reverse {
			putClockSubscription(mem, 0, 1, 1, uint64(50*time.Millisecond), false)
			putClockSubscription(mem, 48, 2, 1, 500, true)
		} else {
			putClockSubscription(mem, 0, 2, 1, 500, true)
			putClockSubscription(mem, 48, 1, 1, uint64(50*time.Millisecond), false)
		}
		code, count := poll(t, e, mem, 2)
		if code != wasiOK || count != 1 {
			t.Fatalf("reverse=%v: poll = errno %d count %d", reverse, code, count)
		}
		if got := binary.LittleEndian.Uint64(mem[256:]); got != 2 {
			t.Fatalf("reverse=%v: ready userdata = %d, want zero/absolute monotonic event", reverse, got)
		}
	}

	e := newTestPlugin(t, Config{Clocks: fixedClocks{}})
	mem := make([]byte, 512)
	putClockSubscription(mem, 0, 1, 1, uint64(time.Millisecond), false)
	putClockSubscription(mem, 48, 2, 1, uint64(100*time.Millisecond), false)
	code, count := poll(t, e, mem, 2)
	if code != wasiOK || count != 1 || binary.LittleEndian.Uint64(mem[256:]) != 1 {
		t.Fatalf("two-clock poll = errno %d count %d userdata %d", code, count, binary.LittleEndian.Uint64(mem[256:]))
	}
}

func TestPollPipeReadinessRightsAndCancellation(t *testing.T) {
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	defer wr.Close()
	e := newTestPlugin(t, Config{Stdin: rd})
	mem := make([]byte, 512)
	putFDSubscription(mem, 0, 11, 1, 0)
	done := make(chan struct{})
	var code uint64
	var count uint32
	go func() {
		code, count = poll(t, e, mem, 1)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("unreadable pipe reported ready")
	case <-time.After(20 * time.Millisecond):
	}
	if _, err := wr.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("poll did not observe readable pipe")
	}
	if code != wasiOK || count != 1 {
		t.Fatalf("pipe poll = errno %d count %d", code, count)
	}

	e.fs.fds[0].rights &^= rightPollFDReadWrite
	mem = make([]byte, 512)
	putFDSubscription(mem, 0, 12, 1, 0)
	code, count = poll(t, e, mem, 1)
	if code != wasiOK || count != 1 || binary.LittleEndian.Uint16(mem[264:]) != wasiENotcapable {
		t.Fatalf("attenuated poll = errno %d count %d event errno %d", code, count, binary.LittleEndian.Uint16(mem[264:]))
	}

	ctx, cancel := context.WithCancel(context.Background())
	e = newTestPlugin(t, Config{Context: ctx, Clocks: fixedClocks{}})
	mem = make([]byte, 512)
	putClockSubscription(mem, 0, 1, 1, uint64(10*time.Second), false)
	canceled := make(chan uint64, 1)
	go func() {
		code, _ := poll(t, e, mem, 1)
		canceled <- code
	}()
	cancel()
	if code := <-canceled; code != wasiEIntr {
		t.Fatalf("canceled poll errno = %d, want %d", code, wasiEIntr)
	}
}

func TestPerInstanceStateLocksDoNotBlockEachOther(t *testing.T) {
	e := newTestPlugin(t, Config{})
	other, err := e.makeFS(true)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFS(other)

	e.fs.mu.Lock()
	defer e.fs.mu.Unlock()
	progress := make(chan struct{})
	go func() {
		other.mu.Lock()
		other.mu.Unlock()
		close(progress)
	}()
	select {
	case <-progress:
	case <-time.After(time.Second):
		t.Fatal("an operation in one instance blocked an unrelated instance")
	}
}

func setIOVec(mem []byte, table, data uint32, value string) {
	binary.LittleEndian.PutUint32(mem[table:], data)
	binary.LittleEndian.PutUint32(mem[table+4:], uint32(len(value)))
	copy(mem[data:], value)
}

func TestStdioObeysDescriptorCloseRightsAndRenumber(t *testing.T) {
	e := newTestPlugin(t, Config{})
	mem := make([]byte, 256)
	setIOVec(mem, 0, 64, "hello")
	result := make([]uint64, 1)
	e.fdClose(testModule{mem}, []uint64{1}, result)
	e.fdWrite(testModule{mem}, []uint64{1, 0, 1, 32}, result)
	if result[0] != wasiEBadf {
		t.Fatalf("write after stdout close = %d", result[0])
	}

	e = newTestPlugin(t, Config{})
	e.fdFdstatSetRights(testModule{mem}, []uint64{1, rightFDFilestatGet, 0}, result)
	e.fdWrite(testModule{mem}, []uint64{1, 0, 1, 32}, result)
	if result[0] != wasiENotcapable {
		t.Fatalf("write after stdout rights attenuation = %d", result[0])
	}

	f, err := os.CreateTemp(t.TempDir(), "renumber")
	if err != nil {
		t.Fatal(err)
	}
	e = newTestPlugin(t, Config{})
	e.fs.fds[3] = &fdEntry{file: f, rights: rightFDWrite}
	e.fdRenumber(testModule{mem}, []uint64{3, 1}, result)
	e.fdWrite(testModule{mem}, []uint64{1, 0, 1, 32}, result)
	if result[0] != wasiOK {
		t.Fatalf("write through renumbered stdout = %d", result[0])
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(f.Name())
	if err != nil || string(got) != "hello" {
		t.Fatalf("renumbered stdout content = %q, %v", got, err)
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

type shortWriter struct{ calls int }

func (w *shortWriter) Write(p []byte) (int, error) {
	w.calls++
	return len(p) / 2, nil
}

func TestDescriptorIOErrorAndShortWriteSemantics(t *testing.T) {
	e := newTestPlugin(t, Config{Stdin: errorReader{err: syscall.EAGAIN}})
	mem := make([]byte, 256)
	binary.LittleEndian.PutUint32(mem[0:], 64)
	binary.LittleEndian.PutUint32(mem[4:], 8)
	result := make([]uint64, 1)
	e.fdRead(testModule{mem}, []uint64{0, 0, 1, 32}, result)
	if result[0] != wasiEAgain || binary.LittleEndian.Uint32(mem[32:]) != 0 {
		t.Fatalf("EAGAIN read = errno %d bytes %d", result[0], binary.LittleEndian.Uint32(mem[32:]))
	}

	w := new(shortWriter)
	e = newTestPlugin(t, Config{Stdout: w})
	setIOVec(mem, 0, 64, "abcdef")
	setIOVec(mem, 8, 80, "second")
	e.fdWrite(testModule{mem}, []uint64{1, 0, 2, 32}, result)
	if result[0] != wasiOK || binary.LittleEndian.Uint32(mem[32:]) != 3 || w.calls != 1 {
		t.Fatalf("short write = errno %d bytes %d calls %d", result[0], binary.LittleEndian.Uint32(mem[32:]), w.calls)
	}
}

func TestDirectoryIterationDoesNotTruncateLargeDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("creates 20,000 entries")
	}
	root := t.TempDir()
	for i := 0; i < 20_000; i++ {
		name := filepath.Join(root, fmt.Sprintf("entry-%05d", i))
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	e := newTestPlugin(t, Config{Preopens: map[string]string{"/": root}})
	mem := make([]byte, 8192)
	result := make([]uint64, 1)
	var cookie uint64
	count := 0
	for {
		e.fdReaddir(testModule{mem}, []uint64{3, 0, 4096, cookie, 5000}, result)
		if result[0] != wasiOK {
			t.Fatalf("readdir cookie %d: errno %d", cookie, result[0])
		}
		used := binary.LittleEndian.Uint32(mem[5000:])
		if used == 0 {
			break
		}
		var at uint32
		for at+24 <= used {
			n := binary.LittleEndian.Uint32(mem[at+16:])
			if at+24+n > used {
				break
			}
			cookie = binary.LittleEndian.Uint64(mem[at:])
			count++
			at += 24 + n
		}
	}
	if want := 20_002; count != want {
		t.Fatalf("directory entries = %d, want %d", count, want)
	}
}
