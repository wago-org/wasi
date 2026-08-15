# Rust WASI Preview 2 sockets fixture

This fixture attempts a numeric-address TCP connection and verifies that a
host without a network grant returns `PermissionDenied`. Rebuild it with:

```sh
cargo build --release --target wasm32-wasip2 \
  --target-dir /tmp/wago-wasip2-sockets-target
cp /tmp/wago-wasip2-sockets-target/wasm32-wasip2/release/wago-wasip2-sockets.wasm \
  ../rust_sockets.component.wasm
```
