# Rust WASI Preview 1 smoke module

This crate is the source for `../rust_smoke.wasm`. Rebuild it from the
repository root with a Rust toolchain that includes `wasm32-wasip1`:

```sh
cargo build --release --target wasm32-wasip1 \
  --manifest-path p1/testdata/rust-smoke/Cargo.toml
cp p1/testdata/rust-smoke/target/wasm32-wasip1/release/wago-wasip1-smoke.wasm \
  p1/testdata/rust_smoke.wasm
```

The program covers arguments, environment, stdin, stdout, stderr, random
seeding, wall time, monotonic time, and polling through Rust's standard library.
