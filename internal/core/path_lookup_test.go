package core

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func pathLookupFixture(t *testing.T) (string, *Plugin) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub", "deeper"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"file": "root", "sub/file": "nested"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true}}})
	t.Cleanup(e.closeAll)
	return root, e
}

func checkPathLookup(t *testing.T, e *Plugin, name string, wantCode uint64, wantContent string) {
	t.Helper()
	for _, operation := range []string{"open", "stat"} {
		t.Run(operation, func(t *testing.T) {
			mem := make([]byte, len(name)+128)
			copy(mem, name)
			resultPtr := uint64(len(name) + 8)
			result := []uint64{999}
			if operation == "stat" {
				e.pathFilestatGet(testModule{mem}, []uint64{3, 1, 0, uint64(len(name)), resultPtr}, result)
				if result[0] != wantCode {
					t.Fatalf("stat %q: errno %d, want %d", name, result[0], wantCode)
				}
				if wantCode == wasiOK && binary.LittleEndian.Uint64(mem[resultPtr+32:]) != uint64(len(wantContent)) {
					t.Fatalf("stat %q: size %d, want %d", name, binary.LittleEndian.Uint64(mem[resultPtr+32:]), len(wantContent))
				}
				return
			}
			e.pathOpen(testModule{mem}, []uint64{3, 1, 0, uint64(len(name)), 0, rightFDRead, 0, 0, resultPtr}, result)
			if result[0] != wasiOK {
				if result[0] != wantCode {
					t.Fatalf("open %q: errno %d, want %d", name, result[0], wantCode)
				}
				return
			}
			fd := binary.LittleEndian.Uint32(mem[resultPtr:])
			file := e.fs.fds[fd].file
			defer e.fdClose(testModule{mem}, []uint64{uint64(fd)}, result)
			if wantCode != wasiOK {
				t.Fatalf("open %q: success, want errno %d", name, wantCode)
			}
			content, err := io.ReadAll(file)
			if err != nil || string(content) != wantContent {
				t.Fatalf("open %q: content %q, error %v, want %q", name, content, err, wantContent)
			}
		})
	}
}

func TestPathLookupPreservesParentComponentChecks(t *testing.T) {
	_, e := pathLookupFixture(t)
	for _, tc := range []struct {
		name string
		code uint64
	}{
		{"missing/../file", wasiENoent},
		{"file/../file", wasiENotdir},
		{"missing/./../file", wasiENoent},
		{"file/./../file", wasiENotdir},
		{"file/.", wasiENotdir},
		{"sub/../file", wasiOK},
		{"sub/./../file", wasiOK},
		{"sub//../file", wasiOK},
	} {
		t.Run(tc.name, func(t *testing.T) { checkPathLookup(t, e, tc.name, tc.code, "root") })
	}
}

func TestPathLookupRepeatedSeparators(t *testing.T) {
	_, e := pathLookupFixture(t)
	for _, name := range []string{"sub//file", "sub///./file"} {
		t.Run(name, func(t *testing.T) { checkPathLookup(t, e, name, wasiOK, "nested") })
	}
}

func TestPathLookupResolvesLinksBeforeParentComponents(t *testing.T) {
	root, e := pathLookupFixture(t)
	if err := os.Symlink(filepath.FromSlash("sub/deeper"), filepath.Join(root, "control-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	// Creation alone is insufficient on syscall emulators: establish that
	// the host can actually follow a simple relative directory symlink.
	requirePathLookupSymlinkControl(t, e, "control-link")
	for _, tc := range []struct {
		name, target string
		code         uint64
		content      string
	}{
		{"deep-link", "sub/deeper", wasiOK, "nested"},
		{"missing-parent-link", "missing/../sub/deeper", wasiENoent, ""},
		{"file-parent-link", "file/../sub/deeper", wasiENotdir, ""},
		{"root-link", ".", wasiENotcapable, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.Symlink(filepath.FromSlash(tc.target), filepath.Join(root, tc.name)); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			checkPathLookup(t, e, tc.name+"/../file", tc.code, tc.content)
		})
	}
}

func TestPathTimestampLookupChecksParentsBeforeMutation(t *testing.T) {
	root, _ := pathLookupFixture(t)
	e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true}}})
	t.Cleanup(e.closeAll)
	before, err := os.Stat(filepath.Join(root, "file"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		code uint64
	}{
		{"missing/../file", wasiENoent}, {"file/../file", wasiENotdir}, {"sub/..", wasiOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.Chtimes(filepath.Join(root, "file"), before.ModTime(), before.ModTime()); err != nil {
				t.Fatal(err)
			}
			mem := make([]byte, len(tc.name))
			copy(mem, tc.name)
			result := []uint64{999}
			e.pathFilestatSetTimes(testModule{mem}, []uint64{3, 0, 0, uint64(len(tc.name)), 0, 42_000_000_000, 4}, result)
			if result[0] != tc.code {
				t.Errorf("set-times %q: errno %d, want %d", tc.name, result[0], tc.code)
			}
			after, err := os.Stat(filepath.Join(root, "file"))
			if err != nil || !after.ModTime().Equal(before.ModTime()) {
				t.Fatalf("set-times %q changed file: %v, %v", tc.name, after, err)
			}
			if tc.code == wasiOK {
				info, err := os.Stat(root)
				if err != nil || !info.ModTime().Equal(time.Unix(42, 0)) {
					t.Fatalf("parent timestamp = %v, error %v", info, err)
				}
			}
		})
	}
}
