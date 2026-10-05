//go:build linux || darwin

package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnixMutationLiteralBackslashes(t *testing.T) {
	root := t.TempDir()
	e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true, MutateDirectory: true}}})
	t.Cleanup(e.closeAll)
	name := `missing\..\created`
	mem, result := []byte(name), []uint64{999}
	e.pathCreateDirectory(testModule{mem}, []uint64{3, 0, uint64(len(mem))}, result)
	if result[0] != wasiOK {
		t.Fatalf("literal backslash mkdir: errno %d", result[0])
	}
	info, err := os.Stat(filepath.Join(root, name))
	if err != nil || !info.IsDir() {
		t.Fatalf("literal backslash directory: %v, %v", info, err)
	}
	e.pathRemoveDirectory(testModule{mem}, []uint64{3, 0, uint64(len(mem))}, result)
	if result[0] != wasiOK {
		t.Fatalf("literal backslash remove-directory: errno %d", result[0])
	}
	if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
		t.Fatalf("literal backslash directory remains: %v", err)
	}
}
