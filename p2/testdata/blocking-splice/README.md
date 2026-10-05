# Blocking splice fixture

The component acquires stdin/stdout once, then invokes either splice method
through the Canonical ABI. Its `run` export returns the byte count on success;
an error sets bit 63 and includes the stream-error discriminator in bit 0.

```sh
wasm-tools parse p2/testdata/blocking-splice/component.wat \
  -o p2/testdata/blocking_splice.component.wasm
wasm-tools validate p2/testdata/blocking_splice.component.wasm
go test ./p2 -run '^TestBlockingSplice|^TestNonblockingSplice' -count=1
```

[WASI 0.2.0 streams](https://raw.githubusercontent.com/WebAssembly/wasi-io/v0.2.0/wit/streams.wit)
requires blocking-splice to wait until both streams are ready before splicing.
