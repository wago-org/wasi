# Rust WASI Preview 2 smoke component

This crate is the source for `../rust_smoke.component.wasm`. Rebuild it from
the repository root with a Rust toolchain that includes `wasm32-wasip2`:

```sh
cargo build --release --target wasm32-wasip2 \
  --manifest-path p2/testdata/rust-smoke/Cargo.toml
cp p2/testdata/rust-smoke/target/wasm32-wasip2/release/wago-wasip2-smoke.wasm \
  p2/testdata/rust_smoke.component.wasm
```

The program covers arguments, environment, stdin, stdout, stderr, random
seeding, wall time, monotonic time, and polling through Rust's standard library.
