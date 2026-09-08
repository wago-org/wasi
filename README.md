<div align="center">
  <h1><code>wasi</code></h1>
  <p>WASI Preview 1 and Preview 2 for Wago, with explicit host access and guest permissions.</p>
</div>

`github.com/wago-org/wasi` installs an experimental bundle containing Preview 1,
the complete WASI 0.2 command import surface, and the unstable compatibility provider. Preview 1 provides the
flat `wasi_snapshot_preview1` imports, including a capability-scoped filesystem.
Preview 2 runs `wasi:cli/command` components through Wago's Component Model
plugin. Networking imports are complete and fail closed with typed
`access-denied` results. The deprecated `wasi_unstable` module is explicitly a
Preview 1 ABI import-name alias, not a claim of compatibility with every
historical unstable snapshot.

All providers expose an empty guest environment by default. Configure `env`
explicitly when a component needs selected values; the host process environment
is never inherited implicitly.

The plugin has no import-time side effects. Generated Wago runtimes call
`register.Providers()` and activate only the exact providers recorded in
`wago-lock.json`.

## Install

```sh
wago add wago-org/wasi
```

For a package root, `wago add` offers to install everything or lets you choose
individual providers. Explicit provider paths skip that choice:

```sh
wago add wago-org/wasi/p1
wago add wago-org/wasi/p2
```

Non-interactive root installs select everything. Authority grants and contract
bindings remain explicit in the reviewed lock graph.

Configure a bounded preopen and keep stdout/stderr attached to the process:

```sh
wago plugin config github.com/wago-org/wasi/p1 \
  '{"mounts":[{"guest":"/data","host":"/srv/guest-data","read":true}],"maxOpenFiles":256}'
```

Then run a command module. The module path becomes `argv[0]`; trailing values are
the remaining guest arguments.

```sh
wago run command.wasm first second
```

## Snapshots

| Plugin ID | Wasm import module | Status |
| --- | --- | --- |
| `github.com/wago-org/wasi` | All providers below | Experimental bundle |
| `github.com/wago-org/wasi/p1` | `wasi_snapshot_preview1` | Experimental (Beta target) |
| `github.com/wago-org/wasi/p2` | WASI 0.2 `wasi:cli/command` imports | Experimental |
| `github.com/wago-org/wasi/unstable` | Preview 1 ABI under `wasi_unstable` | Deprecated alias |

The root selects all three provider paths. Selecting only `/p2` also selects
`github.com/wago-org/component-model`; the reviewed
lock graph binds the component runtime contract to the WASI command provider.
Core-only Preview 1 users do not load the component runtime.

## Preview 2 Go API

The Preview 2 provider publishes a typed command service. Embedders can also
run against an already leased Component Model service directly:

```go
err := p2.Run(ctx, components, componentBytes, p2.Config{
    Stdin:  strings.NewReader("input\n"),
    Stdout: os.Stdout,
    Stderr: os.Stderr,
    Args:   []string{"first", "second"},
    Env:    []string{"MODE=production"},
    Mounts: []p2.Preopen{{
        GuestPath: "/data", HostPath: "/srv/my-component-data",
        Read: true, Write: true, MutateDirectory: true,
    }},
})
```

The runner finds the component's versioned `wasi:cli/run` export, executes it,
and closes the complete adapter graph before returning. A command error is
reported as `*p2.ExitError`. Filesystem access is limited to configured
preopens; descriptor-relative traversal rejects absolute paths, `..`, and
symlinks. With no preopens, the guest sees no host filesystem. TCP, UDP, and
name lookup are present but return WASI `access-denied` while networking is
disabled, rather than trapping or using ambient host network access.

## Host authorities

The Preview 1 providers request four required, non-inheriting Wago authorities:

| Authority | Scope | Why |
| --- | --- | --- |
| `host.import.define` | exactly `wasi_snapshot_preview1` or `wasi_unstable` | Define that snapshot's host functions |
| `host.caller.identify` | identity only | Keep descriptor tables separate without instance control |
| `host.arguments.read` | this runtime's immutable argv | Implement `args_*` without process-global state |
| `instance.close.observe` | opaque close events | Close the departed guest's files |

There is no runtime, module, invocation, compiler, or managed-instance authority.
Narrowing the import-module grant to an empty or different scope fails closed.

Preview 2 requests only `host.arguments.read`; filesystem paths are supplied as
explicit preopens in its reviewed configuration, and networking remains denied.

The root is a policy-free bundle provider: it requests no authority and depends
on P1, P2, and unstable. P1 and unstable are leaves. P2 depends on the Component
Model plugin and binds its typed command service. Wago validates the complete
dependency and contract graph before registration.

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
| `wasi.path.open` | Open paths with descriptor rights bounded by the configured mount |
| `wasi.path.write` | Path creation and mutation below preopens |
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
| `env` | Up to 4096 explicit `KEY=VALUE` strings | Empty |
| `preopens` | Legacy map of guest paths to host directories with full read/write/mutation rights | None |
| `mounts` | Up to 64 `{guest,host,read,write,mutateDirectory}` rights-aware preopens | None |
| `maxOpenFiles` | 3 to 65536, including stdio and preopens | 1024 |
| `maxIOVecs` | 1 to 65536 Preview 1 iovecs per call | 1024 |
| `maxSubscriptionsPerPoll` | 1 to 65536 Preview 1 subscriptions per call | 1024 |

Preview 2 accepts a nested `limits` object with `maxDescriptors`, `maxStreams`,
`maxDirectoryStreams`, `maxPollables`, `maxPollInputs`,
`maxDirectoryEntryBytes`, and `maxAggregateBufferBytes`. Defaults are 256, 256,
64, 1024, 1024, 1 MiB, and 16 MiB respectively.

Configured preopens are opened during plugin startup. A missing path, a regular
file in place of a directory, or an exhausted descriptor bound fails startup and
rolls the entire plugin transaction back. Instance-close events release that
guest's descriptors; runtime shutdown closes every remaining descriptor.

## Go API

The explicit provider is ordinary data:

```go
provider := p1.Provider()
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

The root `wasi.Imports` API remains a low-level Preview 1 convenience. Equivalent
APIs are available from `p1` and `unstable`; only the imported Wasm module name
changes.

## Syscall coverage

Implemented groups include stdio, args/environment, clocks, random, descriptor
I/O and metadata, preopens, path operations, polling, scheduling, and process
exit. Socket calls and `proc_raise` are linked for ABI compatibility but return
the appropriate unsupported, not-a-socket, or bad-descriptor errno; they do not
receive ambient network or signal access.

Every guest pointer is bounds checked. Preopen path traversal is confined below
the opened directory using Linux `openat2` resolution rules or a Darwin
descriptor walk that opens every component with `O_NOFOLLOW`. Darwin rejects
`path_link` when asked to follow
the source symlink because the platform has no race-free descriptor-based link
operation equivalent to Linux `AT_EMPTY_PATH`.

## Compatibility and testing

Preview 1 and unstable support `linux/amd64`, `linux/arm64`, `darwin/amd64`, and
`darwin/arm64`. Preview 2, and therefore the complete root bundle, support
`darwin/arm64`, `linux/amd64`, and `linux/arm64`. All require Go 1.22 or newer and Wago 0.1.0 or
newer.

```sh
go test ./...
go test -race ./...
go vet ./...
```

The checked-in integration fixtures are built from `p1/testdata/rust-smoke`
and the Rust crates under `p2/testdata` with Rust's `wasm32-wasip1` and
`wasm32-wasip2` targets. They exercise real Rust stdio, filesystem, socket
denial, stdin, arguments, environment, random
seeding, clocks, and polling on Wago rather than synthetic WAT alone.

The hermetic suite covers the host boundary, descriptor rights and lifecycle,
path confinement, malformed memory, polling, strict plugin configuration, exact
authority grants, bundle dependencies, and the explicit catalog. CI additionally
pins the official Preview 1 testsuite and WASI 0.2 WIT revision as release gates.

## License

Apache-2.0. See [LICENSE](./LICENSE).
