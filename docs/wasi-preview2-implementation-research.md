# WASI Preview 2 implementation baseline

Status: researched 2026-08-07. This note defines what “full WASI Preview 2”
means for Wago and the gates required before claiming compatibility.

## Decision

Target the latest Preview 2 release, **WASI 0.2.12**, at commit
[`281ba75f`](https://github.com/WebAssembly/WASI/tree/281ba75fafcd50961ef55f9e52747afcc9b71ede).
Vendor or generate bindings from that tag's WIT, rather than hand-maintaining a
look-alike API. Version 0.2.12 promoted `wasi:cli/exit.exit-with-code` to the
stable surface; the remaining changes from 0.2.11 are package/dependency version
updates. The `clocks-timezone` feature remains annotated `@unstable` and is not
part of the default stable target. See the canonical
[`wasi:cli`](https://github.com/WebAssembly/WASI/tree/v0.2.12/proposals/cli/wit)
and [`wasi:clocks`](https://github.com/WebAssembly/WASI/tree/v0.2.12/proposals/clocks/wit)
WIT.

“Full” has two separately instantiable worlds:

1. **`wasi:cli/command@0.2.12`**: the command world, including CLI, clocks,
   filesystem, I/O, random, and sockets imports and exporting
   `wasi:cli/run.run`.
2. **`wasi:http/proxy@0.2.12`**: the HTTP proxy world, including its supporting
   clock, random, stdio, and outgoing HTTP imports and exporting
   `wasi:http/incoming-handler.handle`.

Implementing only `command` is full command-world support, not full WASI 0.2.
The authoritative world compositions are
[`command.wit`](https://github.com/WebAssembly/WASI/blob/v0.2.12/proposals/cli/wit/command.wit)
and
[`proxy.wit`](https://github.com/WebAssembly/WASI/blob/v0.2.12/proposals/http/wit/proxy.wit).

WASI 0.2 is described as stable by the WASI project, but its underlying
Component Model is still published as a **Developer Preview**, not a finalized
WebAssembly 1.0 standard. “Standards compliant” here therefore means exact
compatibility with the pinned WASI 0.2.12 WIT and the Component Model 0.2
feature set. The Component Model project identifies 0.2 as including
shared-nothing/shared-everything linking, high-level value types, resources, and
WIT; native async, futures, and streams are 0.3 additions and must not leak into
the 0.2 decoder or ABI contract. See the Component Model
[`README`](https://github.com/WebAssembly/component-model/blob/73b7ad5d60aab54abf04e53d0bff8c9561caf2b4/README.md)
and its
[`gated-features` section](https://github.com/WebAssembly/component-model/blob/73b7ad5d60aab54abf04e53d0bff8c9561caf2b4/design/mvp/Explainer.md#gated-features).

## Required engine substrate

The existing Wago runtime accepts Core WebAssembly modules. WASI 0.2 guests are
Components, so a host package alone cannot implement this target. Wago must
first provide a Component engine with all of the following:

- component binary decoding, malformed-input rejection, validation, and
  instantiation;
- component/core-module/component-instance index spaces, imports, exports,
  aliases, nested components, and both linking forms in the 0.2 binary format;
- WIT component types: records, variants, enums, flags, tuples, lists, options,
  results, strings, `own`, and `borrow`, plus structural and nominal resource
  type identity;
- Canonical ABI `canon lift`, `canon lower`, `canon resource.new`,
  `canon resource.drop`, and `canon resource.rep`;
- canonical options for UTF-8, UTF-16, and Latin-1+UTF-16 strings, memory,
  `realloc`, and `post-return`;
- per-instance resource handle tables with destructors, ownership transfer,
  dynamic borrow scopes, child-resource relationships, and deterministic
  cleanup when an instance/store closes;
- typed host linking and typed invocation of component exports.

The normative formats and semantics are the Component Model
[`Binary Format`](https://github.com/WebAssembly/component-model/blob/73b7ad5d60aab54abf04e53d0bff8c9561caf2b4/design/mvp/Binary.md),
[`WIT specification`](https://github.com/WebAssembly/component-model/blob/73b7ad5d60aab54abf04e53d0bff8c9561caf2b4/design/mvp/WIT.md),
[`Linking model`](https://github.com/WebAssembly/component-model/blob/73b7ad5d60aab54abf04e53d0bff8c9561caf2b4/design/mvp/Linking.md), and
[`Canonical ABI`](https://github.com/WebAssembly/component-model/blob/73b7ad5d60aab54abf04e53d0bff8c9561caf2b4/design/mvp/CanonicalABI.md).

Do not implement Preview 2 as flat Core Wasm imports. That would run selected
adapted modules but would not load or type-check a Component and cannot provide
the resource ownership or Canonical ABI semantics required by the standard.

## Stable package and interface inventory

The exact signatures, variants, flags, error mappings, and method state
machines must come from the linked 0.2.12 WIT. This table is an implementation
routing map, not a substitute for those definitions.

| Package | Stable interfaces required | Main responsibility |
| --- | --- | --- |
| `wasi:cli@0.2.12` | `environment`, `exit`, `stdin`, `stdout`, `stderr`, `terminal-input`, `terminal-output`, `terminal-stdin`, `terminal-stdout`, `terminal-stderr`, exported `run` | Arguments/environment/current directory, stdio resources, terminal identity, non-returning exit, command entry point |
| `wasi:clocks@0.2.12` | `monotonic-clock`, `wall-clock` | Wall time, monotonic instants/resolution, duration/instant pollables |
| `wasi:io@0.2.12` | `error`, `poll`, `streams` | Error resources, pollables, input/output stream state machines, blocking and nonblocking operations |
| `wasi:filesystem@0.2.12` | `types`, `preopens` | Descriptor and directory-stream resources, descriptor-relative file and directory operations, metadata and preopens |
| `wasi:random@0.2.12` | `random`, `insecure`, `insecure-seed` | Cryptographically secure bytes/u64 and explicitly insecure per-instance randomness |
| `wasi:sockets@0.2.12` | `network`, `instance-network`, `tcp`, `tcp-create-socket`, `udp`, `udp-create-socket`, `ip-name-lookup` | Network capability, TCP/UDP state machines and options, DNS resolution streams |
| `wasi:http@0.2.12` | `types`, `outgoing-handler`, exported `incoming-handler` | HTTP fields, request/response/body/future resources, outbound requests and inbound proxy handling |

Primary WIT packages:
[`io`](https://github.com/WebAssembly/WASI/tree/v0.2.12/proposals/io/wit),
[`filesystem`](https://github.com/WebAssembly/WASI/tree/v0.2.12/proposals/filesystem/wit),
[`random`](https://github.com/WebAssembly/WASI/tree/v0.2.12/proposals/random/wit),
[`sockets`](https://github.com/WebAssembly/WASI/tree/v0.2.12/proposals/sockets/wit), and
[`http`](https://github.com/WebAssembly/WASI/tree/v0.2.12/proposals/http/wit).

Important edge semantics include:

- I/O `poll` traps on an empty list, returns indices (including correct handling
  of duplicate pollables), and stream writes may not exceed the permit returned
  by `check-write`. Blocking operations have the same externally visible result
  without requiring the host thread itself to block. These rules are specified
  in [`poll.wit`](https://github.com/WebAssembly/WASI/blob/v0.2.12/proposals/io/wit/poll.wit)
  and [`streams.wit`](https://github.com/WebAssembly/WASI/blob/v0.2.12/proposals/io/wit/streams.wit).
- Filesystem paths are relative to descriptor capabilities; `path-flags`,
  descriptor/open flags, read-only preopens, timestamps, hard links, metadata
  hashes, stream offsets, and directory iteration are distinct semantic cases.
  See [`filesystem/types.wit`](https://github.com/WebAssembly/WASI/blob/v0.2.12/proposals/filesystem/wit/types.wit).
- TCP and UDP resources have explicit transition rules (unbound, binding,
  bound, connecting, connected, listening, and closed as applicable). Invalid
  transitions return the specified errors; they are not silently accepted. See
  [`tcp.wit`](https://github.com/WebAssembly/WASI/blob/v0.2.12/proposals/sockets/wit/tcp.wit)
  and [`udp.wit`](https://github.com/WebAssembly/WASI/blob/v0.2.12/proposals/sockets/wit/udp.wit).
- HTTP field validation/mutability, single-consumption bodies, single-setting
  response out-parameters, timeout options, futures, and request/response
  lifecycle rules are part of the API contract. See
  [`http/types.wit`](https://github.com/WebAssembly/WASI/blob/v0.2.12/proposals/http/wit/types.wit).
- The HTTP proxy world permits 0..N calls to the exported handler and allows a
  host to reuse or discard instances arbitrarily, so request state must never
  leak between invocations. The proxy world's stdin import must be an EOF stream.

## Safety and robustness requirements

### Component and Canonical ABI boundary

All component input is hostile. Decode vector lengths and nested structures
with explicit size/depth budgets before allocation. Reject unknown, gated, or
malformed encodings; never partially instantiate an invalid component.

Canonical lowering/lifting must check guest-memory bounds, alignment, and
integer overflow before every read, write, or allocation. Validate Unicode
scalar values, string encodings, variant/enum discriminants, flags, and resource
table indices. The ABI sets maximum string/list byte lengths to `2^28 - 1`,
limits flattened synchronous parameters to 16 and results to 1, and prescribes
traps for invalid values. Follow the algorithms rather than relying on Go
casts/slicing behavior. See
[`Loading`](https://github.com/WebAssembly/component-model/blob/73b7ad5d60aab54abf04e53d0bff8c9561caf2b4/design/mvp/CanonicalABI.md#loading),
[`Storing`](https://github.com/WebAssembly/component-model/blob/73b7ad5d60aab54abf04e53d0bff8c9561caf2b4/design/mvp/CanonicalABI.md#storing), and
[`Flattening`](https://github.com/WebAssembly/component-model/blob/73b7ad5d60aab54abf04e53d0bff8c9561caf2b4/design/mvp/CanonicalABI.md#flattening).

Resource types are generative and handles are typed. An `own` transfer removes
the source handle; a `borrow` is valid only for its dynamic call scope; an owner
with outstanding lends cannot be dropped; destructors run once; stale or
wrong-type handles trap. Parent resources cannot be dropped while live child
resources depend on them. These are runtime safety properties, not optional
host-library checks. See the Canonical ABI
[`Resource State`](https://github.com/WebAssembly/component-model/blob/73b7ad5d60aab54abf04e53d0bff8c9561caf2b4/design/mvp/CanonicalABI.md#resource-state)
and the reference
[`resource tests`](https://github.com/WebAssembly/component-model/tree/73b7ad5d60aab54abf04e53d0bff8c9561caf2b4/test/resources).

Enforce Canonical ABI reentrancy and `post-return` rules exactly. Guest traps,
host errors, exit, cancellation, and instance close must unwind tables and host
resources without double-drop, leaks, or cross-instance handle reuse.

### Capability policy

Use a deny-by-default, per-instance context:

- no preopened directories, inherited environment/arguments/stdio, network,
  DNS, or outbound HTTP unless explicitly configured;
- preopens carry independent read/write permissions and all filesystem
  operations remain descriptor-relative and race-safe under concurrent symlink
  and rename mutation; never authorize with a check-then-use host pathname;
- network authority is narrower than “network enabled”: filter bind, listen,
  connect, and every DNS result by address/port, reject multicast/broadcast and
  private/link-local/loopback destinations unless granted, and re-check the
  actual destination used after resolution to prevent DNS rebinding;
- outbound HTTP applies scheme, authority, resolved-address, port, header, body,
  redirect, timeout, and connection-count policy. Prevent request smuggling by
  rejecting invalid/forbidden hop-by-hop and framing headers rather than
  forwarding ambiguous messages;
- secure random uses a cryptographic source. `insecure` and `insecure-seed` are
  separate per-instance facilities and must not alias the secure generator or
  expose host/global seeds.

This shape follows Wasmtime's first-party default: closed stdin, sink
stdout/stderr, empty args/environment/preopens, host clocks, secure random, and
TCP/UDP objects whose addresses plus DNS are denied by default. See
[`WasiCtxBuilder`](https://github.com/bytecodealliance/wasmtime/blob/3ebfbe5af4927c157d6fcaca42b8dbb6d17b73fb/crates/wasi/src/ctx.rs).

### Resource-exhaustion policy

Every guest-controlled resource requires a configurable per-instance bound:
component nesting/decode bytes, resources/handles, open files/directories,
pollables, streams, sockets, DNS results, concurrent HTTP requests/connections,
headers and individual field sizes, buffered body bytes, individual I/O/random
request sizes, timers, and total blocking work. Quota failures must use the
specified WASI error or a deterministic trap and must leave resource state
unchanged.

No WASI call may hold a global runtime lock while performing host I/O or waiting.
Blocking APIs need context cancellation and deadlines. Close/exit/trap must
cancel pending work and release all descriptors, sockets, timers, buffers, and
resource-table entries owned by that instance.

Wasmtime's first-party P2 tests deliberately exercise maximum/many resources,
bad handles, child-resource drop traps, oversized UDP sends, read-only
filesystems, permission-crossing links/renames, pollable traps, socket state
machines, and HTTP limits. The portable sources are in its
[`test-programs` P2 corpus](https://github.com/bytecodealliance/wasmtime/tree/3ebfbe5af4927c157d6fcaca42b8dbb6d17b73fb/crates/test-programs/src/bin)
and the host harness is in
[`crates/wasi/tests/all/p2`](https://github.com/bytecodealliance/wasmtime/tree/3ebfbe5af4927c157d6fcaca42b8dbb6d17b73fb/crates/wasi/tests/all/p2).

## Conformance and acceptance gates

There is currently no official WASI 0.2 suite in `WebAssembly/wasi-testsuite`:
its current scope is Preview 1 and Preview 3. Do not report the Preview 1 suite
as P2 conformance. See its
[`README`](https://github.com/WebAssembly/wasi-testsuite/blob/a4c1c4228d9f83d609354b3b339cf95bf912812c/README.md).

Use these gates instead:

1. **Component Model reference tests.** Port/run the 0.2 subset of the official
   WAST corpus: `binary`, `linking`, `resources`, `validation`, and `values`.
   Exclude tests using explicitly gated post-0.2 features. The corpus and its
   invocation guidance are in the Component Model
   [`test` directory](https://github.com/WebAssembly/component-model/tree/73b7ad5d60aab54abf04e53d0bff8c9561caf2b4/test).
2. **Canonical ABI oracle tests.** Run the executable algorithms' tests from
   [`design/mvp/canonical-abi`](https://github.com/WebAssembly/component-model/tree/73b7ad5d60aab54abf04e53d0bff8c9561caf2b4/design/mvp/canonical-abi),
   and add differential tests against `wasm-tools`/Wasmtime for generated values
   and malformed component binaries.
3. **WASI command integration.** Port Wasmtime's P2 guest programs for CLI,
   clocks, filesystem, streams/poll, random, TCP, UDP, and DNS. Test both direct
   `wasm32-wasip2` components and Preview-1 modules wrapped with the official
   `wasi_snapshot_preview1.command.wasm` adapter.
4. **WASI HTTP integration.** Port Wasmtime's P2 HTTP field/request/response,
   streaming, inbound proxy, outbound request, invalid input, timeout, large
   body, and forwarding tests. The first-party harness is
   [`crates/wasi-http/tests/all/p2`](https://github.com/bytecodealliance/wasmtime/tree/3ebfbe5af4927c157d6fcaca42b8dbb6d17b73fb/crates/wasi-http/tests/all/p2).
5. **Adversarial tests.** Fuzz component decoding/validation and Canonical ABI
   lifting/lowering; race symlink/rename changes; use stale, duplicate,
   wrong-type, borrowed, and parent/child handles; exhaust every configured
   quota; cancel every blocking operation; close instances concurrently; probe
   SSRF/DNS-rebinding and HTTP framing/header attacks.
6. **Engineering gates.** `go test ./...`, `go test -race ./...`, fuzz smoke
   runs, `go vet`, static security analysis, vulnerability scanning, and leak
   checks must pass. CI must run at least one real Rust `wasm32-wasip2` command
   component and one HTTP proxy component end to end.

Compatibility can be called **full WASI 0.2.12** only when both worlds and every
stable interface above are linked and exercised, the Component Model 0.2
reference corpus is green with no semantic skips, the ported P2 command and HTTP
corpora are green, and all authority defaults and quotas fail closed. Until
then, documentation should name the implemented world/interfaces and label the
surface experimental or partial.

## Recommended implementation order

1. Component decoder, validator, linker, typed invocation, and 0.2 WAST tests.
2. Canonical ABI values plus resources and their reference tests/fuzzing.
3. `wasi:io`, then CLI/clocks/random, establishing per-instance context and
   cleanup.
4. Descriptor-capability filesystem using the hardened P1 backend where its
   semantics match, with P2 resource and stream wrappers.
5. Deny-by-default TCP/UDP/DNS with explicit endpoint policy.
6. `wasi:cli/command` end-to-end and Preview-1 adapter compatibility.
7. HTTP types/lifecycle, outbound policy, then the `wasi:http/proxy` export.
8. Differential, adversarial, race, fuzz, quota, cancellation, and leak gates.

This ordering keeps host authority behind a validated, ownership-safe Component
boundary and avoids cementing a host API around an incomplete ABI.
