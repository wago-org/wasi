# Rust WASI Preview 2 filesystem fixture

This fixture is retained as Rust source. Rebuild it with:

```sh
cargo build --release --target wasm32-wasip2 \
  --target-dir /tmp/wago-wasip2-filesystem-target
cp /tmp/wago-wasip2-filesystem-target/wasm32-wasip2/release/wago-wasip2-filesystem.wasm \
  ../rust_filesystem.component.wasm
```
