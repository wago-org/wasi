# wasi

A WASI `preview1` implementation for the
[wago](https://github.com/wago-org/wago) WebAssembly engine. Covers the syscalls a
`wasm32-wasip1` command-line program uses over stdio: standard streams, args, env,
clock, random, and process exit. Filesystem, sockets, and polling are stubbed (see
[Status](#status)).

## Setup

As a library:

```console
$ go get github.com/wago-org/wasi
```

Or add it to a wago project from the registry:

```console
$ wago pkg add github.com/wago-org/wasi
```

The plugin is also compiled into the `wago` binary, so the CLI needs no install.

Wire it onto a `Runtime`:

```go
rt := wago.NewRuntime()
rt.Use(wasi.Init(wasi.Config{Stdout: os.Stdout, Args: os.Args[1:], Env: os.Environ()}))
in, _ := rt.Instantiate(ctx, mod)
in.Invoke("_start")
```

...or run a module from the command line:

```console
$ wago run --plugin wasi program.wasm arg1 arg2
```

Pin a specific snapshot by import path: `github.com/wago-org/wasi/p1`
(`wasi_snapshot_preview1`, the default) or `github.com/wago-org/wasi/unstable`
(`wasi_unstable`, the pre-preview1 ABI).

## Status

**Implemented** — `fd_write` / `fd_read` (stdio), `fd_close`, `fd_seek` (`ESPIPE`),
`fd_fdstat_get`, `args_*`, `environ_*`, `clock_time_get` / `clock_res_get`,
`random_get`, `proc_exit`, plus benign no-ops (`sched_yield`, `fd_sync`, …).

**Stubbed** — the filesystem (`path_*`, real `fd_*`), sockets (`sock_*`), and
`poll_oneoff` return `ENOSYS` / `ENOTSUP` / `EBADF`. A module that links the whole
snapshot still instantiates; the stubbed calls fail at call time, not at load.

Preview 2 (`p2`, the component model) is reserved but not yet implemented.

## License

Apache-2.0. See [LICENSE](LICENSE).
