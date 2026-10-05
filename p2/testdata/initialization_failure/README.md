# Initialization cleanup fixture

This component obtains `get-directories` during core initialization and then
reports an initialization failure. It does not export a command entry point.
The Linux regression verifies that `p2.Run` releases the acquired preopen.

Rebuild and validate the checked-in binary with:

```sh
wasm-tools parse component.wat -o component.wasm
wasm-tools validate component.wasm
```

The regression runs once in a subprocess with a five-second bound. This keeps
any descriptor leaked by a failing dependency isolated to that process; process
exit releases the descriptor. The test uses `/proc/self/fd` to count only handles
to its own temporary mount.
