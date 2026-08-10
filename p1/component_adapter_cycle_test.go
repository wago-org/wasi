package p1_test

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/binary"
	"testing"

	"github.com/wago-org/wago"
	"github.com/wago-org/wasi/p1"
)

//go:embed testdata/cycle-main.wasm
var cycleMain []byte

//go:embed testdata/cycle-shim.wasm
var cycleShim []byte

//go:embed testdata/cycle-proxy.wasm
var cycleProxy []byte

//go:embed testdata/cycle-fixup.wasm
var cycleFixup []byte

//go:embed testdata/cycle-adapter.wasm
var cycleAdapter []byte

//go:embed testdata/blocking-proxy.wasm
var blockingProxy []byte

//go:embed testdata/blocking-fixup.wasm
var blockingFixup []byte

func TestRealCommandThroughLateBoundPreview1Table(t *testing.T) {
	ctx := context.Background()
	rt := wago.NewRuntime()
	defer rt.Close()
	compile := func(wasm []byte) *wago.Module {
		t.Helper()
		m, err := rt.Compile(wasm)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	instantiate := func(wasm []byte, imports wago.Imports) *wago.Instance {
		t.Helper()
		in, err := rt.Instantiate(ctx, compile(wasm), wago.WithImports(imports), wago.WithSynchronousHostCalls())
		if err != nil {
			t.Fatal(err)
		}
		return in
	}
	export := func(in *wago.Instance, name string) *wago.InstanceExport {
		t.Helper()
		fn, err := in.ExportedFunc(name)
		if err != nil {
			t.Fatal(err)
		}
		return fn
	}

	shim := instantiate(cycleShim, nil)
	defer shim.Close()
	main := instantiate(cycleMain, wago.Imports{
		"wasi_snapshot_preview1.fd_write":          export(shim, "fd_write"),
		"wasi_snapshot_preview1.environ_get":       export(shim, "environ_get"),
		"wasi_snapshot_preview1.environ_sizes_get": export(shim, "environ_sizes_get"),
		"wasi_snapshot_preview1.proc_exit":         export(shim, "proc_exit"),
	})
	defer main.Close()
	memory, err := main.ExportedMemory("memory")
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	proxyImports := p1.Imports(p1.Config{Stdout: &stdout})
	proxyImports["env.memory"] = memory
	proxy := instantiate(cycleProxy, proxyImports)
	defer proxy.Close()
	table, err := shim.ExportedTable("table")
	if err != nil {
		t.Fatal(err)
	}
	fixup := instantiate(cycleFixup, wago.Imports{
		"env.table":             table,
		"env.fd_write":          export(proxy, "fd_write"),
		"env.environ_get":       export(proxy, "environ_get"),
		"env.environ_sizes_get": export(proxy, "environ_sizes_get"),
		"env.proc_exit":         export(proxy, "proc_exit"),
	})
	defer fixup.Close()

	if _, err := main.Invoke("_start"); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "hello world\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestPreview1AdapterFDWriteMayResumeAfterHostCall(t *testing.T) {
	ctx := context.Background()
	rt := wago.NewRuntime()
	defer rt.Close()
	compile := func(wasm []byte) *wago.Module {
		t.Helper()
		m, err := rt.Compile(wasm)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	instantiate := func(mod *wago.Module, imports wago.Imports) *wago.Instance {
		t.Helper()
		in, err := rt.Instantiate(ctx, mod, wago.WithImports(imports), wago.WithSynchronousHostCalls())
		if err != nil {
			t.Fatal(err)
		}
		return in
	}
	export := func(in *wago.Instance, name string) *wago.InstanceExport {
		t.Helper()
		fn, err := in.ExportedFunc(name)
		if err != nil {
			t.Fatal(err)
		}
		return fn
	}

	shim := instantiate(compile(cycleShim), nil)
	defer shim.Close()
	main := instantiate(compile(cycleMain), wago.Imports{
		"wasi_snapshot_preview1.fd_write":          export(shim, "fd_write"),
		"wasi_snapshot_preview1.environ_get":       export(shim, "environ_get"),
		"wasi_snapshot_preview1.environ_sizes_get": export(shim, "environ_sizes_get"),
		"wasi_snapshot_preview1.proc_exit":         export(shim, "proc_exit"),
	})
	defer main.Close()
	memory, err := main.ExportedMemory("memory")
	if err != nil {
		t.Fatal(err)
	}
	adapterModule := compile(cycleAdapter)
	var sink bytes.Buffer
	rawBlocking := wago.HostFunc(func(_ wago.HostModule, params, _ []uint64) {
		mem := memory.Bytes()
		_, _ = sink.Write(mem[uint32(params[1]) : uint32(params[1])+uint32(params[2])])
		binary.LittleEndian.PutUint32(mem[uint32(params[3]):], 0)
	})
	proxy := instantiate(compile(blockingProxy), nil)
	defer proxy.Close()
	proxyTable, err := proxy.ExportedTable("table")
	if err != nil {
		t.Fatal(err)
	}
	proxyFixup := instantiate(compile(blockingFixup), wago.Imports{"env.table": proxyTable, "env.host": rawBlocking})
	defer proxyFixup.Close()
	proxyWrite := export(proxy, "write")
	var adapter *wago.Instance
	imports := wago.Imports{
		"env.memory":                   memory,
		"__main_module__._start":       export(main, "_start"),
		"__main_module__.cabi_realloc": export(main, "cabi_realloc"),
	}
	for _, spec := range adapterModule.Imports() {
		if spec.Kind != wago.ImportFunc {
			continue
		}
		key := spec.Key()
		if imports[key] != nil {
			continue
		}
		if key == "wasi:io/streams@0.2.3.[method]output-stream.blocking-write-and-flush" {
			imports[key] = proxyWrite
			continue
		}
		imports[key] = wago.HostFunc(func(mod wago.HostModule, params, results []uint64) {
			mem := mod.Memory()
			switch key {
			case "wasi:cli/stdin@0.2.3.get-stdin":
				results[0] = 1
			case "wasi:cli/stdout@0.2.3.get-stdout":
				results[0] = 2
			case "wasi:cli/stderr@0.2.3.get-stderr":
				results[0] = 3
			case "wasi:filesystem/preopens@0.2.3.get-directories", "wasi:cli/environment@0.2.3.get-environment":
				ptr := uint32(1)
				if key == "wasi:filesystem/preopens@0.2.3.get-directories" {
					out, callErr := adapter.InvokeFromHost(ctx, mod, "cabi_import_realloc", 0, 0, 4, 0)
					if callErr != nil {
						panic(wago.HostTrap{Err: callErr})
					}
					ptr = uint32(out[0])
				}
				binary.LittleEndian.PutUint32(mem[uint32(params[0]):], ptr)
				binary.LittleEndian.PutUint32(mem[uint32(params[0])+4:], 0)
			case "wasi:io/streams@0.2.3.[method]output-stream.blocking-write-and-flush":
				_, _ = sink.Write(mem[uint32(params[1]) : uint32(params[1])+uint32(params[2])])
				binary.LittleEndian.PutUint32(mem[uint32(params[3]):], 0)
			}
		})
	}
	adapter = instantiate(adapterModule, imports)
	defer adapter.Close()
	table, err := shim.ExportedTable("table")
	if err != nil {
		t.Fatal(err)
	}
	fixup := instantiate(compile(cycleFixup), wago.Imports{
		"env.table":             table,
		"env.fd_write":          export(adapter, "fd_write"),
		"env.environ_get":       export(adapter, "environ_get"),
		"env.environ_sizes_get": export(adapter, "environ_sizes_get"),
		"env.proc_exit":         export(adapter, "proc_exit"),
	})
	defer fixup.Close()
	if _, err := adapter.Invoke("wasi:cli/run@0.2.3#run"); err != nil {
		t.Fatal(err)
	}

	const dataPtr, iovPtr, writtenPtr = 1 << 20, 1<<20 + 32, 1<<20 + 64
	copy(memory.Bytes()[dataPtr:], "hello world\n")
	binary.LittleEndian.PutUint32(memory.Bytes()[iovPtr:], dataPtr)
	binary.LittleEndian.PutUint32(memory.Bytes()[iovPtr+4:], 12)
	got, err := adapter.Invoke("fd_write", 1, iovPtr, 1, writtenPtr)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || uint32(got[0]) != 0 {
		t.Fatalf("fd_write = %v", got)
	}
}
