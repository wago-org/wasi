//go:build windows

package core

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/wago-org/wasi/internal/winfs"
	"golang.org/x/sys/windows"
)

func requirePathLookupSymlinkControl(t *testing.T, e *Plugin, name string) {
	t.Helper()
	h, err := winfs.OpenAt(windows.Handle(e.fs.fds[3].file.Fd()), name, os.O_RDONLY, 0, true, false, false)
	if err != nil {
		t.Skipf("native directory symlink unsupported: %v", err)
	}
	file := os.NewFile(uintptr(h), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.IsDir() {
		t.Skipf("native directory symlink unsupported: %v", err)
	}
}

func TestWindowsPathTimestampParentStepRetainsMutationAccess(t *testing.T) {
	root, _ := pathLookupFixture(t)
	e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true}}})
	t.Cleanup(e.closeAll)
	sub := filepath.Join(root, "sub")
	unchanged := time.Unix(100, 0)
	want := time.Unix(42, 0)
	for _, name := range []string{"sub", "sub/.", "sub/deeper/..", "sub/deeper/../.", "sub/deeper/../../sub"} {
		t.Run(name, func(t *testing.T) {
			for _, path := range []string{root, sub, filepath.Join(sub, "deeper")} {
				if err := os.Chtimes(path, unchanged, unchanged); err != nil {
					t.Fatal(err)
				}
			}
			mem := []byte(name)
			var result [1]uint64
			e.pathFilestatSetTimes(testModule{mem}, []uint64{3, 0, 0, uint64(len(name)), 0, uint64(want.UnixNano()), 4}, result[:])
			if result[0] != wasiOK {
				t.Fatalf("no-follow set-times %q: errno %d", name, result[0])
			}
			for _, path := range []string{root, sub, filepath.Join(sub, "deeper")} {
				expected := unchanged
				if path == sub {
					expected = want
				}
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if !info.ModTime().Equal(expected) {
					t.Fatalf("%s mtime = %v, want %v", path, info.ModTime(), expected)
				}
			}
		})
	}
}

func TestWindowsPinnedParentDirectoryOpenRetainsRequestedAccess(t *testing.T) {
	_, e := pathLookupFixture(t)
	parent, leaf, code := openParent(e.fs.fds[3], "sub/deeper/..")
	if code != wasiOK {
		t.Fatalf("pin parent: errno %d", code)
	}
	defer parent.Close()
	query := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQueryInformationFile")
	access := func(handle windows.Handle) uint32 {
		t.Helper()
		var granted uint32
		var status windows.IO_STATUS_BLOCK
		// FileAccessInformation reports the access granted to this handle.
		result, _, _ := syscall.SyscallN(query.Addr(), uintptr(handle), uintptr(unsafe.Pointer(&status)), uintptr(unsafe.Pointer(&granted)), unsafe.Sizeof(granted), 8)
		if result != 0 {
			t.Fatal(windows.NTStatus(result))
		}
		return granted
	}
	if granted := access(windows.Handle(parent.Fd())); granted&windows.FILE_WRITE_ATTRIBUTES != 0 {
		t.Fatalf("read-only parent control has write-attributes access: %#x", granted)
	}
	// Establish that a native directory open can acquire the requested
	// access before exercising the data opener's pinned-dot path.
	control, err := winfs.OpenMetadataAtAccess(windows.Handle(parent.Fd()), ".", true, windows.FILE_WRITE_ATTRIBUTES)
	if err != nil {
		t.Fatalf("native directory-access control: %v", err)
	}
	defer windows.CloseHandle(control)
	if granted := access(control); granted&windows.FILE_WRITE_ATTRIBUTES == 0 {
		t.Fatalf("native directory-access control dropped attributes access: %#x", granted)
	}
	h, err := winfs.OpenAtAccess(windows.Handle(parent.Fd()), leaf, os.O_RDONLY, 0, true, false, true, windows.FILE_WRITE_ATTRIBUTES)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if granted := access(h); granted&windows.FILE_WRITE_ATTRIBUTES == 0 {
		t.Fatalf("requested write-attributes access was dropped: %#x", granted)
	}
}

func TestWindowsParentTimestampMutationStaysPinnedAfterRename(t *testing.T) {
	root, e := pathLookupFixture(t)
	parent, leaf, code := openParent(e.fs.fds[3], "sub/deeper/..")
	if code != wasiOK {
		t.Fatalf("pin parent: errno %d", code)
	}
	defer parent.Close()
	sub, moved := filepath.Join(root, "sub"), filepath.Join(root, "moved")
	if err := os.Rename(sub, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	unchanged := time.Unix(100, 0)
	if err := os.Chtimes(sub, unchanged, unchanged); err != nil {
		t.Fatal(err)
	}
	want := time.Unix(42, 0)
	if err := setPathTimes(parent, leaf, []time.Time{want, want}, true); err != nil {
		t.Fatal(err)
	}
	for path, expected := range map[string]time.Time{moved: want, sub: unchanged} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(expected) {
			t.Fatalf("%s mtime = %v, want %v", path, info.ModTime(), expected)
		}
	}
}

func TestWindowsParentWalkClosesPinnedHandles(t *testing.T) {
	_, e := pathLookupFixture(t)
	query := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")
	count := func() uint32 {
		t.Helper()
		var handles uint32
		result, _, err := syscall.SyscallN(query.Addr(), uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&handles)))
		if result == 0 {
			t.Fatalf("GetProcessHandleCount: %v", err)
		}
		return handles
	}
	name := strings.Repeat("sub/../", 16) + "file"
	before := count()
	if before == 0 {
		t.Skip("process handle counts are unavailable on this host")
	}
	for i := 0; i < 100; i++ {
		file, code := openAt(e.fs.fds[3], name, hostOpenReadOnly, 0)
		if code != wasiOK {
			t.Fatalf("open %d: errno %d", i, code)
		}
		_ = file.Close()
	}
	after := count()
	t.Logf("process handles before %d, after %d", before, after)
	if after > before+8 {
		t.Fatalf("parent walk leaked handles: before %d, after %d", before, after)
	}
}
