# wasi

A minimal [WASI](https://wasi.dev/) host interface for wasm guests, packaged as a
[wago](https://github.com/wago-org/wago) plugin. It gives a `_start` command-line
program the surface it actually needs: the standard streams, process exit, and
args / env / clock / random.

That is enough to run real `wasm32-wasip1` programs (Rust, C, Go, AssemblyScript)
that talk to stdio. The filesystem, sockets, and polling are **not** implemented —
but their imports are present and return a clean `errno`, so a module that links
the whole snapshot still instantiates and fails gracefully at the call, not at load.

```go
rt := wago.NewRuntime()
rt.Use(wasi.Ext(wasi.Config{Stdout: os.Stdout, Args: os.Args[1:], Env: os.Environ()}))
mod, _ := rt.Compile(src)
in, _ := rt.Instantiate(ctx, mod)
in.Invoke("_start")            // hello, wasi
```

## Pick a version by its import path

Every version shares one implementation (`internal/core`); only the wasm module
name and the extension identity differ.

| Import path | Package | wasm module | Notes |
|---|---|---|---|
| `github.com/wago-org/wasi` | `wasi` | `wasi_snapshot_preview1` | **Default.** Re-exports preview 1, the common case. |
| `github.com/wago-org/wasi/p1` | `p1` | `wasi_snapshot_preview1` | Preview 1, explicitly. |
| `github.com/wago-org/wasi/unstable` | `unstable` | `wasi_unstable` | Pre-preview1 "snapshot 0", for older toolchains. |
| `github.com/wago-org/wasi/p2` | `p2` | — | Placeholder for preview 2 (component model); not implemented. |

`import "github.com/wago-org/wasi"` gives you plain `wasi.Ext` / `wasi.Imports` /
`wasi.Config` bound to `wasi_snapshot_preview1`. To pin a specific snapshot, import
the versioned subpackage by its full path instead.

## Library usage

Two ways to wire it in.

**As a plugin on a Runtime** — capability-gated and inspectable:

```go
rt := wago.NewRuntime()
rt.Use(wasi.Ext(wasi.Config{Stdout: os.Stdout, Args: os.Args[1:], Env: os.Environ()}))
in, _ := rt.Instantiate(ctx, mod)
in.Invoke("_start")
```

**As a raw host-import bundle** — on the low-level `Instantiate` path:

```go
in, _ := wago.Instantiate(c, wasi.Imports(wasi.Config{Stdout: os.Stdout}))
in.Invoke("_start")
```

`proc_exit` surfaces as a `*wago.ExitError` whose `Code` is the guest's exit
status — a clean exit, not a trap:

```go
if _, err := in.Invoke("_start"); err != nil {
    var ex *wago.ExitError
    if errors.As(err, &ex) {
        os.Exit(int(ex.Code))
    }
    // otherwise a real trap
}
```

Pin the older ABI by importing the subpackage:

```go
import "github.com/wago-org/wasi/unstable"
in, _ := wago.Instantiate(c, unstable.Imports(unstable.Config{Stdout: os.Stdout}))
```

### Config

Every field is optional; the zero value is a silent, deterministic sandbox (no
output, immediate EOF on stdin, fixed clock, `crypto/rand`).

```go
type Config struct {
    Stdout, Stderr io.Writer    // nil discards
    Stdin          io.Reader    // nil is immediate EOF
    Args           []string     // argv; Args[0] is conventionally the program name
    Env            []string     // "KEY=VALUE" entries
    Now            func() int64 // wall-clock ns for clock_time_get; nil is a fixed clock
    Rand           io.Reader    // random source; nil uses crypto/rand
}
```

## CLI usage

WASI is one of the plugins compiled into the `wago` binary — there is no dedicated
flag. A module that exports `_start` runs as a command; add `--plugin wasi` to give
it the WASI host interface (argv, env, and stdio are wired from the process, and
`proc_exit` becomes the process exit code):

```console
$ wago run --plugin wasi program.wasm arg1 arg2       # default: preview1
$ wago run --plugin wasi/p1 program.wasm              # pin preview1 explicitly
$ wago run --plugin wasi/unstable old-program.wasm    # pre-preview1 ABI
```

The plugin name is a path: `wasi` is the default (preview1), and `wasi/<version>`
selects a specific snapshot. `wasi/p2` is reserved for preview 2 and errors until
it is implemented.

Inspect it like any other plugin:

```console
$ wago plugin list
$ wago plugin inspect wasi        # identity, the `wasi` capability, every import + signature
```

## Capability

The whole surface is guarded by one capability, `wasi` (`wago.CapWASI`). With no
policy it is permitted; a `wago.Policy` can allow- or deny-list it to sandbox what a
module may reach.

## Coverage

**Implemented:** `fd_write` / `fd_read` (stdio), `fd_close`, `fd_seek` (ESPIPE),
`fd_fdstat_get`, `args_*`, `environ_*`, `clock_time_get` / `clock_res_get`,
`random_get`, `proc_exit`, plus benign no-ops (`sched_yield`, `fd_sync`, …).

**Stubbed** (return `ENOSYS` / `ENOTSUP` / `EBADF` so a module still instantiates):
the filesystem (`path_*`, real `fd_*`), sockets (`sock_*`), and `poll_oneoff`.

Conformance is tracked against the
[WebAssembly/wasi-testsuite](https://github.com/WebAssembly/wasi-testsuite)
(`p1/wasitest_exec_test.go`, gated on `WAGO_WASITEST_DIR`) and a corpus of real
Rust/WASI programs (`p1/wasi_apps_test.go`).

## Manifest

[`wago-plugin.json`](wago-plugin.json) is this module's manifest, modeled on
`package.json`: shared module metadata (name, version, provenance, `engines`) plus a
`subpackages` map of the extensions it ships — the data a registry or catalog reads
without compiling. (Host-import signatures are deliberately left out; discover those
by compiling or via `wago plugin inspect`.) It is generated from the code, so
regenerate it after changing extension identity:

```console
$ go generate ./...        # or: go run ./internal/genmanifest
```

## Layout

```
.                     default package: re-exports p1 as wasi_snapshot_preview1
├── p1/               wasi_snapshot_preview1 extension (+ the test corpus)
├── unstable/         wasi_unstable "snapshot 0" extension
├── p2/               preview 2 placeholder (no API yet)
└── internal/
    ├── core/         the shared host-function implementation
    └── genmanifest/  generator for wago-plugin.json
```

Each directory has its own README with the details.

## License

Apache-2.0. See [LICENSE](LICENSE).
