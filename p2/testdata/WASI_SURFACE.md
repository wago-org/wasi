# WASI 0.2.0 surface pin

`wasi-0.2.0.surface` is generated from `WebAssembly/wasi` tag `v0.2.0`, commit
`70214b878af4ce45889b4ad9d26a7ac98db8931b`:

```sh
go run ./cmd/wit-surface -root /path/to/wasi/preview2 > p2/testdata/wasi-0.2.0.surface
```

The exported `wasi:cli/run` interface is omitted because this provider hosts
the command world's imports; the guest implements that export.
