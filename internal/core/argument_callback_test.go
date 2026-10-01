package core

import (
	"testing"

	"github.com/wago-org/wago"
	"github.com/wago-org/wago/tests/support/wasmtest"
)

func argumentGrowModule(name string) []byte {
	imp := append(wasmtest.Name("wasi_snapshot_preview1"), wasmtest.Name(name)...)
	imp = append(imp, 0, 0)
	run := []byte{0, 0x20, 0, 0x20, 1, 0x10, 0, 0x0b}
	grow := []byte{0, 0x41, 1, 0x40, 0, 0x0b}
	body := func(code []byte) []byte { return append(wasmtest.ULEB(uint32(len(code))), code...) }
	return wasmtest.Module(
		wasmtest.Section(1, wasmtest.Vec([]byte{0x60, 2, 0x7f, 0x7f, 1, 0x7f}, []byte{0x60, 0, 1, 0x7f})),
		wasmtest.Section(2, wasmtest.Vec(imp)),
		wasmtest.Section(3, []byte{2, 0, 1}),
		wasmtest.Section(5, []byte{1, 1, 1, 2}),
		wasmtest.Section(7, wasmtest.Vec(wasmtest.ExportEntry("run", 0, 1), wasmtest.ExportEntry("grow", 0, 2), wasmtest.ExportEntry("memory", 2, 0))),
		wasmtest.Section(10, wasmtest.Vec(body(run), body(grow))),
	)
}

func TestArgumentCallbackGrowthAndIsolation(t *testing.T) {
	for _, name := range []string{"args_sizes_get", "args_get", "environ_sizes_get", "environ_get"} {
		t.Run(name, func(t *testing.T) {
			c, err := wago.Compile(nil, argumentGrowModule(name))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = c.Close() })
			for _, value := range []string{"first", "independent-longer"} {
				value := value
				t.Run(value, func(t *testing.T) {
					t.Parallel()
					configuration := value
					if name == "environ_sizes_get" || name == "environ_get" {
						configuration = "KEY=" + value
					}
					items := []string{configuration}
					imports := Imports("wasi_snapshot_preview1", Config{Args: items, Env: items})
					items[0] = "caller mutation"
					in, err := wago.Instantiate(c, wago.InstantiateOptions{Imports: imports})
					if err != nil {
						t.Fatal(err)
					}
					defer in.Close()
					fn, err := in.WasmFunc("run")
					if err != nil {
						t.Fatal(err)
					}
					check := func(want uint64) {
						t.Helper()
						r, err := fn.Invoke(65536, 65568)
						if err != nil || len(r) != 1 || r[0] != want {
							t.Fatalf("call=%v error=%v want=%d", r, err, want)
						}
					}
					check(wasiEFault)
					r, err := in.Invoke("grow")
					if err != nil || len(r) != 1 || r[0] != 1 {
						t.Fatalf("grow=%v error=%v", r, err)
					}
					check(wasiOK)
					if name == "args_sizes_get" || name == "environ_sizes_get" {
						count, countOK := in.ReadUint32Le(65536)
						size, sizeOK := in.ReadUint32Le(65568)
						if !countOK || !sizeOK || count != 1 || size != uint32(len(configuration)+1) {
							t.Fatal("wrong instance configuration")
						}
					} else {
						mem, ok := in.Read(65568, uint32(len(configuration)+1))
						if !ok || string(mem) != configuration+"\x00" {
							t.Fatal("wrong instance string")
						}
					}
					if err := in.Close(); err != nil {
						t.Fatal(err)
					}
					if _, err := fn.Invoke(0, 64); err == nil {
						t.Fatal("call after close succeeded")
					}
				})
			}
		})
	}
}
