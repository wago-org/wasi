# Third-party test notices

This repository includes tests and test fixtures adapted from the following
projects:

- [wazero](https://github.com/tetratelabs/wazero), copyright The wazero
  Authors, Apache License 2.0. Preview 1 syscall cases and the filesystem
  adapter behavior are derived from its WASI test suite and public
  `experimental/sys` interfaces.
- [Wasmtime](https://github.com/bytecodealliance/wasmtime), copyright the
  Bytecode Alliance contributors, Apache License 2.0 with LLVM exception.
  The Preview 2 differential fixtures use Wasmtime output as their reference
  behavior.
- [Wazy](https://github.com/samyfodil/wazy), copyright its contributors,
  Apache License 2.0. Preview 2 integration cases and compiled component
  fixtures were adapted from its WASI Preview 2 suite.

The original license and copyright terms continue to apply to adapted
material. See each linked upstream repository for its full license text.
