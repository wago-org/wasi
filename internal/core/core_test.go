package core

import (
	"context"
	"encoding/binary"
	"os"
	"reflect"
	"testing"

	wago "github.com/wago-org/wago"
)

type testModule struct{ mem []byte }

func (m testModule) Memory() []byte { return m.mem }

func TestPluginUsesRuntimeScopedArguments(t *testing.T) {
	const id = "example.com/test/wasi"
	definition := Definition(id, "Test WASI", "test provider", wago.Experimental, "test.wasi")
	instance := &Plugin{module: "test.wasi"}
	provider := Provider(definition, "test.wasi")
	provider.New = func() wago.Plugin { return instance }
	digest, err := wago.DefinitionDigest(definition)
	if err != nil {
		t.Fatal(err)
	}
	grants := make([]wago.AuthorityGrant, len(definition.Authorities))
	for i, request := range definition.Authorities {
		grants[i] = wago.AuthorityGrant{Name: request.Name, Scope: request.Scope}
	}
	rt := wago.NewRuntime(wago.WithGuestArguments([]string{"guest", "one"}))
	defer rt.Close()
	if err := rt.LoadPlugins(context.Background(), wago.PluginSet{
		Providers:  []wago.PluginProvider{provider},
		Selections: []wago.PluginSelection{{ID: id, DefinitionDigest: digest, Direct: true, Dependencies: map[string]string{}, Grants: grants}},
	}); err != nil {
		t.Fatalf("LoadPlugins: %v", err)
	}
	if got, want := instance.cfg.Args, []string{"guest", "one"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime argv = %v, want %v", got, want)
	}
}

func newTestPlugin(t *testing.T, cfg Config) *Plugin {
	t.Helper()
	e := &Plugin{module: "wasi_snapshot_preview1", cfg: cloneConfig(cfg)}
	e.resetFS()
	if err := e.initFS(false); err != nil {
		t.Fatalf("initFS: %v", err)
	}
	return e
}

func TestPreview1PreopenAndFileLifecycle(t *testing.T) {
	root := t.TempDir()
	e := newTestPlugin(t, Config{Preopens: map[string]string{"/": root}})
	m := testModule{mem: make([]byte, 512)}
	result := make([]uint64, 1)

	e.fdPrestatGet(m, []uint64{3, 0}, result)
	if result[0] != wasiOK || binary.LittleEndian.Uint32(m.mem[4:]) != 1 {
		t.Fatalf("fd_prestat_get = errno %d, name len %d", result[0], binary.LittleEndian.Uint32(m.mem[4:]))
	}
	e.fdPrestatDirName(m, []uint64{3, 8, 1}, result)
	if result[0] != wasiOK || m.mem[8] != '/' {
		t.Fatalf("fd_prestat_dir_name = errno %d, name %q", result[0], m.mem[8:9])
	}

	copy(m.mem[32:], "file")
	e.pathOpen(m, []uint64{3, 0, 32, 4, 1, rightFDRead | rightFDWrite | rightFDSeek | rightFDTell | rightFDFilestatGet, 0, 0, 16}, result)
	if result[0] != wasiOK {
		t.Fatalf("path_open: errno %d", result[0])
	}
	fd := uint64(binary.LittleEndian.Uint32(m.mem[16:]))

	copy(m.mem[96:], "hello")
	binary.LittleEndian.PutUint32(m.mem[64:], 96)
	binary.LittleEndian.PutUint32(m.mem[68:], 5)
	e.fdWrite(m, []uint64{fd, 64, 1, 20}, result)
	if result[0] != wasiOK || binary.LittleEndian.Uint32(m.mem[20:]) != 5 {
		t.Fatalf("fd_write = errno %d, bytes %d", result[0], binary.LittleEndian.Uint32(m.mem[20:]))
	}
	e.fdSeek(m, []uint64{fd, 0, 0, 24}, result)
	if result[0] != wasiOK {
		t.Fatalf("fd_seek: errno %d", result[0])
	}
	binary.LittleEndian.PutUint32(m.mem[72:], 112)
	binary.LittleEndian.PutUint32(m.mem[76:], 5)
	e.fdRead(m, []uint64{fd, 72, 1, 28}, result)
	if result[0] != wasiOK || string(m.mem[112:117]) != "hello" {
		t.Fatalf("fd_read = errno %d, data %q", result[0], m.mem[112:117])
	}
	e.fdFilestatGet(m, []uint64{fd, 128}, result)
	if result[0] != wasiOK || binary.LittleEndian.Uint64(m.mem[160:]) != 5 {
		t.Fatalf("fd_filestat_get = errno %d, size %d", result[0], binary.LittleEndian.Uint64(m.mem[160:]))
	}
	e.fdClose(m, []uint64{fd}, result)
	if result[0] != wasiOK {
		t.Fatalf("fd_close: errno %d", result[0])
	}
	if got, err := os.ReadFile(root + "/file"); err != nil || string(got) != "hello" {
		t.Fatalf("host file = %q, %v", got, err)
	}
}

func TestPreview1RejectsCapabilityEscape(t *testing.T) {
	base := t.TempDir()
	root := base + "/root"
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base+"/outside", []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside", root+"/escape"); err != nil {
		t.Fatal(err)
	}
	e := newTestPlugin(t, Config{Preopens: map[string]string{"/": root}})
	m := testModule{mem: make([]byte, 128)}
	copy(m.mem[32:], "../outside")
	result := make([]uint64, 1)
	e.pathOpen(m, []uint64{3, 0, 32, 10, 0, 0, 0, 0, 16}, result)
	if result[0] != wasiENotcapable {
		t.Fatalf("path_open escape errno = %d, want %d", result[0], wasiENotcapable)
	}
	copy(m.mem[48:], "escape")
	e.pathOpen(m, []uint64{3, 1, 48, 6, 0, rightFDRead, 0, 0, 16}, result)
	if result[0] != wasiENotcapable {
		t.Fatalf("path_open symlink escape errno = %d, want %d", result[0], wasiENotcapable)
	}
}
