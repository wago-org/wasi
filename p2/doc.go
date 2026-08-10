// Package p2 implements the stable WASI Preview 2 interfaces for Wago's
// capability-gated WebAssembly Component Model plugin.
//
// Preview 2 is not a wider set of host functions like the p1/unstable snapshots —
// it is a different model entirely: the guest is a WebAssembly Component (the
// component model) whose imports are WIT interfaces (wasi:cli, wasi:io,
// wasi:filesystem, …) lowered through canonical ABI adapters, not flat
// `(func (param i32 ...) (result i32))` core-wasm imports. Enable installs the
// `wago.component-model` plugin with its explicit `runtime.core` authority; With
// supplies capability-scoped WASI host implementations to an instantiation.
//
// Filesystem, network, HTTP, clocks, random, environment, and process behavior
// remain denied or constrained unless enabled in Config. Component execution
// authority does not itself grant any guest-visible WASI capability.
package p2
