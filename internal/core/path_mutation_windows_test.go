//go:build windows

package core

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func windowsMutationFixture(t *testing.T) (string, *Plugin) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub", "deeper"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"file", "sub/nested"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true, MutateDirectory: true}}})
	t.Cleanup(e.closeAll)
	return root, e
}

func TestWindowsMutationParentLookup(t *testing.T) {
	for _, tc := range []struct {
		name string
		code uint64
	}{
		{"created", wasiOK},
		{`missing\..\created`, wasiENoent},
		{`file\..\created`, wasiENotdir},
		{`sub\..\created`, wasiOK},
		{`sub/..\created`, wasiOK},
		{`sub\deeper\..\created`, wasiOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, e := windowsMutationFixture(t)
			mem, result := []byte(tc.name), []uint64{999}
			e.pathCreateDirectory(testModule{mem}, []uint64{3, 0, uint64(len(mem))}, result)
			if result[0] != tc.code {
				t.Fatalf("mkdir %q: errno %d, want %d", tc.name, result[0], tc.code)
			}
			wantName := "created"
			if strings.Contains(tc.name, "deeper") {
				wantName = "sub/created"
			}
			_, err := os.Stat(filepath.Join(root, filepath.FromSlash(wantName)))
			if tc.code == wasiOK && err != nil || tc.code != wasiOK && !os.IsNotExist(err) {
				t.Fatalf("created directory after errno %d: %v", result[0], err)
			}
		})
	}
}

func TestWindowsMutationParentLeaf(t *testing.T) {
	_, e := windowsMutationFixture(t)
	for _, name := range []string{"file", `sub\nested`, `sub\..\file`, `sub/..\file`, `sub\..`, `sub\..\`} {
		t.Run(name, func(t *testing.T) {
			parent, leaf, code := openParent(e.fs.fds[3], name)
			if parent != nil {
				defer parent.Close()
			}
			if code != wasiOK || leaf == ".." || strings.ContainsAny(leaf, `/\`) {
				t.Fatalf("parent %q: leaf %q, errno %d; want a confined single component", name, leaf, code)
			}
		})
	}
}

func TestWindowsMutationRejectsRootedNames(t *testing.T) {
	for _, name := range []string{`\created`, `\sub\created`, `C:\created`, `C:created`, `\\server\share\created`, `sub\C:created`} {
		t.Run(name, func(t *testing.T) {
			root, e := windowsMutationFixture(t)
			mem, result := []byte(name), []uint64{999}
			e.pathCreateDirectory(testModule{mem}, []uint64{3, 0, uint64(len(mem))}, result)
			if result[0] != wasiENotcapable {
				t.Fatalf("mkdir rooted %q: errno %d, want ENOTCAPABLE", name, result[0])
			}
			for _, candidate := range []string{"created", "sub/created"} {
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(candidate))); !os.IsNotExist(err) {
					t.Fatalf("rooted path mutated %q: %v", candidate, err)
				}
			}
		})
	}
}

func TestWindowsDirectoryMutationTerminalBackslash(t *testing.T) {
	for _, name := range []string{`new\`, `sub\new\`} {
		t.Run(name, func(t *testing.T) {
			root, e := windowsMutationFixture(t)
			mem, result := []byte(name), []uint64{999}
			e.pathCreateDirectory(testModule{mem}, []uint64{3, 0, uint64(len(mem))}, result)
			if result[0] != wasiOK {
				t.Fatalf("mkdir %q: errno %d", name, result[0])
			}
			if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil || !info.IsDir() {
				t.Fatalf("created directory: %v, %v", info, err)
			}
			e.pathRemoveDirectory(testModule{mem}, []uint64{3, 0, uint64(len(mem))}, result)
			if result[0] != wasiOK {
				t.Fatalf("remove-directory %q: errno %d", name, result[0])
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); !os.IsNotExist(err) {
				t.Fatalf("directory remains: %v", err)
			}
		})
	}
}

func TestWindowsDirectoryMutationMixedTerminalSeparators(t *testing.T) {
	for _, name := range []string{`new/\`, `new\/`} {
		t.Run(name, func(t *testing.T) {
			root, e := windowsMutationFixture(t)
			mem, result := []byte(name), []uint64{999}
			e.pathCreateDirectory(testModule{mem}, []uint64{3, 0, uint64(len(mem))}, result)
			if result[0] != wasiOK {
				t.Fatalf("mkdir %q: errno %d, want OK", name, result[0])
			}
			if info, err := os.Stat(filepath.Join(root, "new")); err != nil || !info.IsDir() {
				t.Fatalf("created directory = %v, %v", info, err)
			}
			e.pathRemoveDirectory(testModule{mem}, []uint64{3, 0, uint64(len(mem))}, result)
			if result[0] != wasiOK {
				t.Fatalf("rmdir %q: errno %d, want OK", name, result[0])
			}
			if _, err := os.Stat(filepath.Join(root, "new")); !os.IsNotExist(err) {
				t.Fatalf("directory remains after rmdir: %v", err)
			}
		})
	}
	root, e := windowsMutationFixture(t)
	mem, result := []byte(`sub/\`), []uint64{999}
	e.pathCreateDirectory(testModule{mem}, []uint64{3, 0, uint64(len(mem))}, result)
	if result[0] != wasiEExist {
		t.Fatalf("mkdir existing sub/\\: errno %d, want EEXIST", result[0])
	}
	if data, err := os.ReadFile(filepath.Join(root, "sub", "nested")); err != nil || string(data) != "keep" {
		t.Fatalf("existing directory contents changed: %q, %v", data, err)
	}
}

func TestWindowsRenameDirectoryWithTerminalBackslash(t *testing.T) {
	root, e := windowsMutationFixture(t)
	if err := os.Mkdir(filepath.Join(root, "source"), 0o700); err != nil {
		t.Fatal(err)
	}
	oldName, newName := `source\`, "target"
	mem, result := []byte(oldName+newName), []uint64{999}
	e.pathRename(testModule{mem}, []uint64{3, 0, uint64(len(oldName)), 3, uint64(len(oldName)), uint64(len(newName))}, result)
	if result[0] != wasiOK {
		t.Fatalf("rename directory with terminal backslash: errno %d", result[0])
	}
	if info, err := os.Stat(filepath.Join(root, "target")); err != nil || !info.IsDir() {
		t.Fatalf("target directory: %v, %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(root, "source")); !os.IsNotExist(err) {
		t.Fatalf("source remains: %v", err)
	}
}

func TestWindowsFileMutationRejectsTerminalBackslash(t *testing.T) {
	root, e := windowsMutationFixture(t)
	oldName, newName := `file\`, "target"
	mem, result := []byte(oldName+newName), []uint64{999}
	e.pathUnlinkFile(testModule{mem}, []uint64{3, 0, uint64(len(oldName))}, result)
	if result[0] == wasiOK {
		t.Fatal("unlink accepted file with terminal backslash")
	}
	e.pathRename(testModule{mem}, []uint64{3, 0, uint64(len(oldName)), 3, uint64(len(oldName)), uint64(len(newName))}, result)
	if result[0] == wasiOK {
		t.Fatal("rename accepted file with terminal backslash")
	}
	if content, err := os.ReadFile(filepath.Join(root, "file")); err != nil || string(content) != "keep" {
		t.Fatalf("file changed: %q, %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(root, "target")); !os.IsNotExist(err) {
		t.Fatalf("target created: %v", err)
	}
}

func TestWindowsTerminalParentStepCannotMutateMountRoot(t *testing.T) {
	for _, name := range []string{`sub\..`, `sub\..\`} {
		t.Run(name, func(t *testing.T) {
			root, e := windowsMutationFixture(t)
			mem, result := []byte(name), []uint64{999}
			e.pathRemoveDirectory(testModule{mem}, []uint64{3, 0, uint64(len(mem))}, result)
			if result[0] == wasiOK {
				t.Fatal("removed mount root")
			}
			newName := "moved"
			mem = []byte(name + newName)
			e.pathRename(testModule{mem}, []uint64{3, 0, uint64(len(name)), 3, uint64(len(name)), uint64(len(newName))}, result)
			if result[0] == wasiOK {
				t.Fatal("renamed mount root")
			}
			if _, err := os.Stat(root); err != nil {
				t.Fatalf("mount root missing: %v", err)
			}
			if content, err := os.ReadFile(filepath.Join(root, "file")); err != nil || string(content) != "keep" {
				t.Fatalf("mount contents changed: %q, %v", content, err)
			}
			if _, err := os.Stat(filepath.Join(root, "moved")); !os.IsNotExist(err) {
				t.Fatalf("unexpected rename target: %v", err)
			}
		})
	}
}

func TestWindowsRenameParentLookup(t *testing.T) {
	for _, tc := range []struct {
		old, new string
		code     uint64
	}{
		{"file", "sub/renamed", wasiOK},
		{`missing\..\file`, "renamed", wasiENoent},
		{`file\..\file`, "renamed", wasiENotdir},
		{"file", `missing\..\renamed`, wasiENoent},
		{"file", `file\..\renamed`, wasiENotdir},
		{`sub\..\file`, `sub\deeper\..\renamed`, wasiOK},
	} {
		t.Run(tc.old+"_to_"+tc.new, func(t *testing.T) {
			root, e := windowsMutationFixture(t)
			mem := []byte(tc.old + tc.new)
			result := []uint64{999}
			e.pathRename(testModule{mem}, []uint64{3, 0, uint64(len(tc.old)), 3, uint64(len(tc.old)), uint64(len(tc.new))}, result)
			if result[0] != tc.code {
				t.Fatalf("rename: errno %d, want %d", result[0], tc.code)
			}
			original, err := os.ReadFile(filepath.Join(root, "file"))
			if tc.code != wasiOK && (err != nil || string(original) != "keep") {
				t.Fatalf("failed rename changed source: %q, %v", original, err)
			}
			if tc.code == wasiOK {
				content, err := os.ReadFile(filepath.Join(root, "sub", "renamed"))
				if err != nil || string(content) != "keep" || !os.IsNotExist(errForStat(filepath.Join(root, "file"))) {
					t.Fatalf("renamed file: %q, %v", content, err)
				}
			}
		})
	}
}

func errForStat(name string) error { _, err := os.Stat(name); return err }

func TestWindowsDeleteParentLookup(t *testing.T) {
	for _, tc := range []struct {
		name string
		code uint64
	}{
		{"file", wasiOK},
		{`missing\..\file`, wasiENoent},
		{`file\..\file`, wasiENotdir},
		{`sub\..\file`, wasiOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, e := windowsMutationFixture(t)
			mem, result := []byte(tc.name), []uint64{999}
			e.pathUnlinkFile(testModule{mem}, []uint64{3, 0, uint64(len(mem))}, result)
			if result[0] != tc.code {
				t.Fatalf("unlink: errno %d, want %d", result[0], tc.code)
			}
			content, err := os.ReadFile(filepath.Join(root, "file"))
			if tc.code == wasiOK && !os.IsNotExist(err) || tc.code != wasiOK && (err != nil || string(content) != "keep") {
				t.Fatalf("file after unlink: %q, %v", content, err)
			}
		})
	}
}

func TestWindowsReadlinkParentLookup(t *testing.T) {
	for _, tc := range []struct {
		name string
		code uint64
	}{
		{"link", wasiOK},
		{`missing\..\link`, wasiENoent},
		{`file\..\link`, wasiENotdir},
		{`sub\..\link`, wasiOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, e := windowsMutationFixture(t)
			if err := os.Symlink("file", filepath.Join(root, "link")); err != nil {
				t.Skipf("symlink creation unsupported: %v", err)
			}
			if tc.code == wasiOK {
				// Wine may create a Go-visible link that native NT lookup
				// cannot open. Require an ordinary readlink control before
				// testing successful parent traversal through that fixture.
				control, result := make([]byte, 128), []uint64{999}
				copy(control, "link")
				e.pathReadlink(testModule{control}, []uint64{3, 0, 4, 8, 64, 120}, result)
				if result[0] != wasiOK {
					t.Skipf("native readlink control unsupported: errno %d", result[0])
				}
			}
			mem, result := make([]byte, len(tc.name)+128), []uint64{999}
			copy(mem, tc.name)
			buffer, used := uint64(len(tc.name)), uint64(len(mem)-4)
			e.pathReadlink(testModule{mem}, []uint64{3, 0, uint64(len(tc.name)), buffer, 64, used}, result)
			if result[0] != tc.code {
				t.Fatalf("readlink: errno %d, want %d", result[0], tc.code)
			}
			if tc.code == wasiOK && string(mem[buffer:buffer+uint64(binary.LittleEndian.Uint32(mem[used:]))]) != "file" {
				t.Fatalf("readlink returned wrong target")
			}
		})
	}
}
