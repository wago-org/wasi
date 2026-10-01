package core

import (
	"testing"

	"github.com/wago-org/wago"
)

func TestHostCallDoesNotBoxCaller(t *testing.T) {
	for _, name := range []string{"args_sizes_get", "args_get", "environ_sizes_get", "environ_get"} {
		t.Run(name, func(t *testing.T) {
			c, err := wago.Compile(nil, dispatchTestModule(name, []wago.ValType{wago.ValI32, wago.ValI32}, []wago.ValType{wago.ValI32}))
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			in, err := wago.Instantiate(c, wago.InstantiateOptions{Imports: Imports("wasi_snapshot_preview1", Config{Args: []string{"command", "one"}, Env: []string{"KEY=value"}})})
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			fn, err := in.WasmFunc("run")
			if err != nil {
				t.Fatal(err)
			}
			allocs := testing.AllocsPerRun(1000, func() {
				r, err := fn.Invoke(0, 64)
				if err != nil || len(r) != 1 || r[0] != 0 {
					t.Fatalf("%s: %v %v", name, r, err)
				}
			})
			if allocs != 0 {
				t.Fatalf("host call allocates %.0f objects, want no Caller interface allocation", allocs)
			}
		})
	}
}
