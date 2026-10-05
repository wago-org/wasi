# Blocking output error fixture

This small component calls each blocking output operation through the Canonical
ABI and returns the result and stream-error discriminators. It verifies that
ordinary asynchronous write/flush failures reach the guest as a typed result.
The additional `flush-and-write` export checks that the post-flush probe replaces
the earlier write permit, including shrinking capacity and failed probes.
The checked-in binary is built from the adjacent WAT source:

```sh
wasm-tools parse p2/testdata/blocking-output-errors/component.wat \
  -o p2/testdata/blocking_output_errors.component.wasm
wasm-tools validate p2/testdata/blocking_output_errors.component.wasm
go test ./p2 -run '^TestBlockingOutput' -count=1
```

The signatures follow the repository's pinned
[WASI 0.2.0 streams interface](https://github.com/WebAssembly/WASI/blob/70214b878af4ce45889b4ad9d26a7ac98db8931b/preview2/io/streams.wit).
