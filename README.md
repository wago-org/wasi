<div align="center">
    <h1><code>wasi</code></h1>
    <p>A WASI host interface for the <a href="https://github.com/wago-org/wago">Wago</a> WebAssembly runtime - stdio, args/env, clock, random, and exit for <code>wasm32-wasip1</code> command-line programs.</p>
</div>

<p align="center">
    <a href="https://github.com/wago-org/wasi/actions/workflows/ci.yml"><img src="https://github.com/wago-org/wasi/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
    <a href="https://codecov.io/gh/wago-org/wasi"><img src="https://codecov.io/gh/wago-org/wasi/branch/main/graph/badge.svg" alt="Coverage"></a>
    <a href="https://go.dev/"><img src="https://img.shields.io/badge/go-%3E%3D1.22-00ADD8.svg" alt="Go >= 1.22"></a>
    <a href="https://github.com/wago-org/wago"><img src="https://img.shields.io/badge/wago-%3E%3D0.1.0-6E56CF.svg" alt="Wago >= 0.1.0"></a>
</p>

<details>
<summary>Table of Contents</summary>

- [Overview](#overview)
- [Installation](#installation)
- [Usage](#usage)
  - [On a Runtime](#on-a-runtime)
  - [As a raw import bundle](#as-a-raw-import-bundle)
  - [From the command line](#from-the-command-line)
- [Snapshots](#snapshots)
- [Configuration](#configuration)
- [Syscall support](#syscall-support)
- [Compatibility](#compatibility)
- [Testing](#testing)
- [Architecture](#architecture)
- [Contributing](#contributing)
- [License](#license)
- [Contact](#contact)

</details>

## Overview

`wasi` is the default WASI host interface for [Wago](https://github.com/wago-org/wago).
It implements the slice of [`wasi_snapshot_preview1`](https://github.com/WebAssembly/WASI)
that a `wasm32-wasip1` command-line program actually uses over stdio: the standard
streams, `args`, `environ`, `clock`, `random`, and `proc_exit`. That is enough to run
real programs - Rust, C, TinyGo, and AssemblyScript binaries that read input, compute,
and write output - without a filesystem.

What you get out of the box:

- **The stdio command surface**: `fd_write` / `fd_read`, `args_*`, `environ_*`,
  `clock_*`, `random_get`, and `proc_exit`, wired to plain `io.Writer` / `io.Reader`
  and `[]string` you supply.
- **Bounds-checked by construction**: every guest pointer is validated against linear
  memory; a malformed pointer returns `EINVAL`, never a host-side panic that would
  abort the instance.
- **Graceful degradation**: the filesystem, sockets, and polling are stubbed with a
  clean errno (`ENOSYS` / `ENOTSUP` / `EBADF`). A module that links the whole snapshot
  still instantiates; the unimplemented calls fail at call time, not at load.
- **Snapshot-versioned**: pin `wasi_snapshot_preview1` (default) or the older
  `wasi_unstable` ABI by import path; preview 2 has a reserved slot.

> **Stability:** the `p1` snapshot is **stable**; `unstable` is **deprecated** (kept for
> old toolchains); `p2` is a **placeholder**. See [Snapshots](#snapshots).

## Installation

If you have the [`wago`](https://github.com/wago-org/wago) CLI installed:

```sh
wago pkg add github.com/wago-org/wasi
```

or use [`go get`](https://pkg.go.dev/cmd/go#hdr-Get_packages_and_dependencies):

```sh
go get github.com/wago-org/wasi
```

The plugin is also compiled into the `wago` binary, so the CLI needs no separate
install.

The whole WASI surface is guarded by one capability, `wago.CapWASI`. With no policy it
is permitted; under a policy (strict mode) allow it explicitly to let a guest reach any
WASI import.

## Usage

### On a Runtime

Wire the extension onto a `Runtime` - capability-gated and inspectable:

```go
package main

import (
	"os"

	"github.com/wago-org/wago"
	"github.com/wago-org/wasi"
)

func main() {
	rt := wago.NewRuntime()
	rt.Use(wasi.Init(wasi.Config{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Args:   os.Args[1:],
		Env:    os.Environ(),
	}))
	defer rt.Close()

	c, _ := wago.Compile(nil, moduleBytes)
	in, _ := rt.Instantiate(ctx, c)
	in.Invoke("_start")
}
```

`wasi.Init` binds `wasi_snapshot_preview1` - the common case. To pin a specific snapshot,
construct it from the versioned subpackage instead (see [Snapshots](#snapshots)).

### As a raw import bundle

On the low-level `wago.Instantiate(c, imports)` path, `Imports` returns the host bundle
directly, keyed `"<module>.<name>"`:

```go
in, _ := wago.Instantiate(c, wago.InstantiateOptions{
	Imports: wasi.Imports(wasi.Config{Stdout: os.Stdout}),
})
in.Invoke("_start")
```

### From the command line

```sh
wago run --plugin github.com/wago-org/wasi program.wasm arg1 arg2
```

The CLI feeds the process's own stdio and environment to the guest and forwards the
trailing arguments as the guest's `argv`.

## Snapshots

WASI has shipped under more than one wasm import module name. Each is a subpackage with
an identical Go API (`Init` / `Imports` / `Config`); they differ only in the module name
they bind under and their identity metadata.

| Import path | Wasm module | Stability | When you need it |
| --- | --- | --- | --- |
| `github.com/wago-org/wasi` | `wasi_snapshot_preview1` | stable | Default. Re-exports `p1`; use this unless you have a reason not to. |
| `github.com/wago-org/wasi/p1` | `wasi_snapshot_preview1` | stable | The current ABI emitted by `wasm32-wasip1` toolchains (Rust, C, TinyGo, AssemblyScript). |
| `github.com/wago-org/wasi/unstable` | `wasi_unstable` | deprecated | "Snapshot 0", the pre-preview1 ABI of older toolchains. Function-for-function identical to `p1` over this surface. |
| `github.com/wago-org/wasi/p2` | - | placeholder | WASI preview 2 (the component model). Reserves the slot; **not yet implemented**. |

Preview 2 is a different model entirely - the guest is a WebAssembly **Component** whose
imports are WIT interfaces lowered through the canonical ABI, not flat core-wasm imports.
It needs a component-model loader Wago does not have yet; run preview1 modules with `p1`
until then.

## Configuration

`wasi.Config` (an alias of the shared `core.Config`) is the whole knob surface. Every
field is optional and has a sensible zero behavior.

| Field | Type | Zero value behaves as |
| --- | --- | --- |
| `Stdout`, `Stderr` | `io.Writer` | discards writes (reports them as written) |
| `Stdin` | `io.Reader` | clean EOF on `fd_read` |
| `Args` | `[]string` | empty `argv` (`Args[0]` is conventionally the program name) |
| `Env` | `[]string` | empty environment (`"KEY=VALUE"` entries) |
| `Now` | `func() int64` | a fixed clock - deterministic, handy for tests |
| `Rand` | `io.Reader` | `crypto/rand.Reader` |

## Syscall support

**Implemented** - the stdio command surface, fully wired:

| Group | Functions |
| --- | --- |
| Streams | `fd_write`, `fd_read`, `fd_close`, `fd_seek` (`ESPIPE`), `fd_fdstat_get` |
| Process | `proc_exit` |
| Args / env | `args_sizes_get`, `args_get`, `environ_sizes_get`, `environ_get` |
| Clock | `clock_time_get`, `clock_res_get` |
| Random | `random_get` |

**No-op** - benign hints, flushes, and cooperative yields return success:
`sched_yield`, `fd_advise`, `fd_datasync`, `fd_sync`, `fd_fdstat_set_flags`.

**Stubbed** - the filesystem (`path_*`, real `fd_*`), sockets (`sock_*`), polling
(`poll_oneoff`), and timers return a clean errno (`ENOSYS` / `ENOTSUP` / `EBADF` /
`ESPIPE`) rather than a missing-import failure. Each is a concrete growth target, not a
dead end.

## Compatibility

| Axis | Support |
| --- | --- |
| Wago engine | `>= 0.1.0` |
| Go toolchain | `>= 1.22` |
| TinyGo | compatible (the library builds under TinyGo; the test harnesses are `!tinygo`) |
| Platforms | `linux/amd64` (the tier the corpus/suite tests run on) |

Identity, engine constraints, and platform tags live in one place - `wago.json` at the
module root - and are loaded at runtime via `wasi.Info`, keyed by module path. The same
file doubles as the manifest a registry reads, so runtime identity and the catalog never
drift.

## Testing

```sh
go test ./...
```

The default suite is self-contained: `TestWASIHelloWorld` runs a hand-assembled
`wasi_snapshot_preview1` module end to end (no external toolchain), and the `unstable`
package mirrors it for the older ABI.

Two larger tiers are gated so a plain `go test` stays fast and hermetic:

- **Real application corpus** (`TestWASIApps`, `linux && amd64`) executes the checked-in
  Rust/WASI binaries under `../wago/bench/corpus` - pulldown-cmark, blake3, serde_json,
  the rhai scripting engine, `regex`, `num-bigint` - and asserts each program's
  deterministic output. It skips any binary that isn't present.
- **Conformance oracle** (`TestWASISuite`) runs the
  [WebAssembly/wasi-testsuite](https://github.com/WebAssembly/wasi-testsuite) preview1
  corpus when `WAGO_WASITEST_DIR` points at a checkout. Tests needing a filesystem
  preopen, sockets, or an unimplemented feature are skipped; the rest must match their
  manifest's exit code and stdout.

The preview 1 corpus benchmarks compare Wago with Wazero's compiler runtime on
the same application binaries. Run them on Darwin or Linux `amd64`/`arm64` with:

```sh
go test -run '^$' -bench '^Benchmark(Wazero)?WASI' -benchmem -count=1 -benchtime=2000ms ./p1
```

## Architecture

- **`wasi.go`** - the module root: the default (`wasi_snapshot_preview1`) `Init` /
  `Imports`, plus `Info`, which resolves extension identity from `wago.json` (self-similar
  manifest, subpackages inherit parent metadata).
- **`internal/core/`** - the shared implementation. The minimal snapshot surface is
  identical across `wasi_unstable` and `wasi_snapshot_preview1`, so both wrap this
  package with only a different module name. Holds every host function and its
  bounds-checked memory helpers.
- **`p1/`**, **`unstable/`** - thin wrappers that bind `core` under their module name and
  identity. `p2/` marks the reserved preview-2 slot.
- **`register/`** - a blank-import shim (`import _ ".../wasi/register"`) that wires the
  WASI plugins into Wago's global registry for `wago plugin build`.
- **`wago.json`** - the package manifest declaring the module, its subpackages, engines,
  and platforms.

## Contributing

Contributions are welcome! Please:

- Run `go test ./...` and `go vet ./...` before opening a pull request.
- Turn a stubbed syscall into a real one where it makes sense - each `ENOSYS` in
  `internal/core` is a labeled growth target.
- Follow standard Go formatting (`gofmt`) and conventional commit messages.

## License

This project is distributed under the [Apache License 2.0](./LICENSE). Work on this
project is done out of passion - if you want to support it financially, you can donate
through [GitHub Sponsors](https://github.com/sponsors/JairusSW).

## Contact

Please file issues at [GitHub Issues](https://github.com/wago-org/wasi/issues). To chat,
join the [Wago Discord](https://wago.sh/discord).

- **GitHub:** [https://github.com/wago-org/](https://github.com/wago-org/)
- **Website:** [https://wago.sh/](https://wago.sh/)
- **Discord:** [https://wago.sh/discord](https://wago.sh/discord)
