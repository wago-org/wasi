<div align="center">
  <h1><code>wasi</code></h1>
  <p>WASI Preview 1 for Wago, with explicit host access and guest permissions.</p>
</div>

`github.com/wago-org/wasi` implements the `wasi_snapshot_preview1` command ABI:
stdio, argv and environment, clocks, random, polling, process exit, and a
capability-scoped filesystem. The deprecated `wasi_unstable` module is available
for old toolchains. Preview 2 is reserved but not implemented.

The plugin has no import-time side effects. Generated Wago runtimes call
`register.Providers()` and activate only the exact providers recorded in
`wago-lock.json`.

## Install

```sh
wago add github.com/wago-org/wasi
```

`wago add` reviews the immutable definition, its exact authority requests, and
the resolved lock graph before changing the project. For non-interactive setup:

```sh
wago plugin grant github.com/wago-org/wasi \
  --allow host.import.define,host.caller.identify,host.arguments.read,instance.close.observe
```

Configure a bounded preopen and keep stdout/stderr attached to the process:

```sh
wago plugin config github.com/wago-org/wasi \
  '{"preopens":{"/data":"/srv/guest-data"},"maxOpenFiles":256,"maxPollDurationMillis":1000}'
```

Then run a command module. The module path becomes `argv[0]`; trailing values are
the remaining guest arguments.

```sh
wago run command.wasm first second
```

## Snapshots

| Plugin ID | Wasm import module | Status |
| --- | --- | --- |
| `github.com/wago-org/wasi` | `wasi_snapshot_preview1` | Stable default |
| `github.com/wago-org/wasi/p1` | `wasi_snapshot_preview1` | Stable, version-pinned package |
| `github.com/wago-org/wasi/unstable` | `wasi_unstable` | Deprecated compatibility package |

The Go package `github.com/wago-org/wasi/p2` remains a source placeholder, not
a plugin package. It is deliberately absent from `wago.json` and cannot be
published or resolved until it has a real provider.

The root and `/p1` providers expose the same Wasm module. Select one of them.
Wago rejects a lock graph that selects both before either plugin starts.

## Host authorities

Every provider requests four required, non-inheriting Wago authorities:

| Authority | Scope | Why |
| --- | --- | --- |
| `host.import.define` | exactly `wasi_snapshot_preview1` or `wasi_unstable` | Define that snapshot's host functions |
| `host.caller.identify` | identity only | Keep descriptor tables separate without instance control |
| `host.arguments.read` | this runtime's immutable argv | Implement `args_*` without process-global state |
| `instance.close.observe` | opaque close events | Close the departed guest's files |

There is no runtime, module, invocation, compiler, or managed-instance authority.
Narrowing the import-module grant to an empty or different scope fails closed.

WASI is a leaf plugin: it declares no plugin dependency and provides or consumes
no typed cross-plugin contract. It can still share a runtime with contract-based
plugins because Wago validates the complete dependency and contract graph before
registration. Its provider does not retain another plugin's values or call across
plugin lifetimes.

## Guest capabilities

Host authorities govern what the Go plugin may do to Wago. Guest capabilities
govern which imported functions a WebAssembly module may exercise. WASI labels
every import with one of these narrower capabilities:

| Capability | Surface |
| --- | --- |
| `wasi.fd.read` | Stream and descriptor reads |
| `wasi.fd.write` | Stream and descriptor writes |
| `wasi.fd.manage` | Descriptor close, seek, stat, rights, and renumbering |
| `wasi.path.read` | Path metadata and symlink reads below preopens |
| `wasi.path.write` | Path open/create/mutation below preopens |
| `wasi.arguments.read` | Guest argv |
| `wasi.environment.read` | Guest environment |
| `wasi.clock.read` | Clock resolution and time |
| `wasi.random.read` | Cryptographic randomness |
| `wasi.process.exit` | `proc_exit` |
| `wasi.poll` | `poll_oneoff` |
| `wasi.scheduler.yield` | `sched_yield` |
| `wasi.unsupported` | Compatibility stubs for signals and sockets |

For programmatic instantiation, allow only what the module needs:

```go
instance, err := runtime.Instantiate(ctx, module, wago.WithPolicy(wago.Policy{
    AllowedCapabilities: []wago.Capability{
        wasi.CapFDWrite,
        wasi.CapArgumentsRead,
        wasi.CapProcessExit,
    },
}))
```

Preview 1 multiplexes stdout and files through the same descriptor syscalls, so
`wasi.fd.read` and `wasi.fd.write` intentionally describe descriptor operations,
not a misleading per-resource distinction. Filesystem reach is independently
bounded by the configured preopens and WASI descriptor rights.

## Configuration

Plugin configuration is strict JSON. Unknown fields, `null`, relative host paths,
unclean guest paths, malformed environment entries, trailing JSON, and limits
outside the documented ranges are rejected before the provider factory runs.

| Field | Values | Default |
| --- | --- | --- |
| `stdin` | `"inherit"` or `"eof"` | `"inherit"` |
| `stdout`, `stderr` | `"inherit"` or `"discard"` | `"inherit"` |
| `env` | Up to 4096 `KEY=VALUE` strings | Host process environment |
| `preopens` | Up to 64 clean absolute guest paths mapped to clean absolute host directories | None |
| `maxOpenFiles` | 3 to 65536, including stdio and preopens | 1024 |
| `maxPollDurationMillis` | 1 to 60000 | 1000 |

Configured preopens are opened during plugin startup. A missing path, a regular
file in place of a directory, or an exhausted descriptor bound fails startup and
rolls the entire plugin transaction back. Instance-close events release that
guest's descriptors; runtime shutdown closes every remaining descriptor.

## Go API

The explicit provider is ordinary data:

```go
provider := wasi.Provider()
digest, err := wago.DefinitionDigest(provider.Definition)
if err != nil {
    return err
}

grants := make([]wago.AuthorityGrant, len(provider.Definition.Authorities))
for i, request := range provider.Definition.Authorities {
    grants[i] = wago.AuthorityGrant{Name: request.Name, Scope: request.Scope}
}

runtime := wago.NewRuntime(wago.WithGuestArguments([]string{"command.wasm", "first"}))
defer runtime.Close()
err = runtime.LoadPlugins(ctx, wago.PluginSet{
    Providers: []wago.PluginProvider{provider},
    Selections: []wago.PluginSelection{{
        ID: provider.Definition.ID,
        DefinitionDigest: digest,
        Grants: grants,
    }},
})
```

Embedders that deliberately bypass plugin review can keep using the raw import
bundle:

```go
imports := wasi.Imports(wasi.Config{Stdout: os.Stdout, Args: []string{"command.wasm"}})
instance, err := wago.Instantiate(compiled, wago.InstantiateOptions{Imports: imports})
```

The same APIs are available from `p1` and `unstable`; only the imported Wasm
module name changes.

## Syscall coverage

Implemented groups include stdio, args/environment, clocks, random, descriptor
I/O and metadata, preopens, path operations, polling, scheduling, and process
exit. Socket calls and `proc_raise` are linked for ABI compatibility but return
the appropriate unsupported, not-a-socket, or bad-descriptor errno; they do not
receive ambient network or signal access.

Every guest pointer is bounds checked. Preopen path traversal is confined below
the opened directory using Linux `openat2` resolution rules or Darwin's
`O_RESOLVE_BENEATH` open policy. Darwin rejects `path_link` when asked to follow
the source symlink because the platform has no race-free descriptor-based link
operation equivalent to Linux `AT_EMPTY_PATH`.

## Compatibility and testing

The secure filesystem implementation currently supports `linux/amd64`,
`darwin/amd64`, and `darwin/arm64`, Go 1.22 or newer, and Wago 0.1.0 or newer.

```sh
go test ./...
go test -race ./...
go vet ./...
```

The hermetic suite covers the host boundary, descriptor rights and lifecycle,
path confinement, malformed memory, polling, strict plugin configuration, exact
authority grants, atomic provider conflicts, and the explicit catalog. Optional
corpus and wasi-testsuite harnesses remain documented in the test source.

## License

Apache-2.0. See [LICENSE](./LICENSE).
