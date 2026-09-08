# WASI security and conformance review

Review target: `main` at `bb0149491ce503341a96549a6d3ad63608a8ceef`
Recorded: 2026-09-08

This document is the remediation record for the critical review of the root,
Preview 1, Preview 2, and legacy providers. The review compared the checkout
with WASI 0.2.0 WIT, the upstream WASI testsuite, checked-in tests, and CI.
The original reviewer could not execute the checkout because GitHub DNS was
unavailable; defect findings were based on code and specification inspection.

## Verdict

Do not describe or ship the root provider as stable. Preview 1 has broad syscall
coverage and is targeting Beta after conformance qualification. Preview 2 now
registers the complete WASI 0.2 command import surface but remains Experimental.
The descriptor-relative filesystem confinement remains a strong foundation.

## Release blockers

- [x] Default P1 and P2 plugin environments to empty; environment values must be supplied explicitly.
- [x] Add sound read-only preopens and prevent child descriptor rights escalation.
- [x] Remove the Preview 1 runtime-global lock from blocking host I/O.
- [x] Replace Preview 1 `poll_oneoff` fabricated readiness with real, cancellable readiness and correct clock-event selection.
- [x] Separate realtime and monotonic clocks and reject unsupported CPU clocks instead of fabricating values.
- [x] Make Preview 1 stdio ordinary descriptor-table entries.
- [x] Correct Preview 1 partial I/O and expand platform errno translation.
- [x] Register and test the complete pinned WASI 0.2 command import surface, with typed fail-closed networking.
- [x] Replace eager P2 stdin buffering, fake write permits, trapping stream errors, and the shared permanently-ready pollable.
- [x] Add per-instance quotas for descriptors, streams, directory streams, pollables, subscriptions, iovecs, directory buffering, and aggregate buffers.
- [x] Make P2 append atomic across streams and reject `u64` offsets above `math.MaxInt64`.
- [x] Replace P1's 16,384-entry directory snapshot with incremental iteration.
- [x] Add a secure Linux `openat2` fallback, platform allocation/sync helpers, expanded errno tests, and Linux/macOS CI.
- [x] Correct stability and completeness claims: the root and P1 are experimental, P2 is an experimental limited profile, and `wasi_unstable` is deprecated.

## Additional correctness work

- [x] Validate and open P2 preopens transactionally before component execution.
- [x] Make P2 `argv[0]` match the documented runtime module path.
- [x] Return real P2 stat timestamps and link counts where the platform exposes them.
- [x] Implement P1 allocation rather than sparse truncation.
- [x] Roll back P1 descriptor flags if the host `fcntl` operation fails.
- [x] Require unlink rights before P1's trailing-slash type probe.
- [x] Propagate cancellation out of P2 poll and reject timer-duration overflow.
- [x] Distinguish P2 `sync-data` from `sync` where supported.
- [x] Name and test the exact historical ABI implemented by `wasi_unstable`.

## Required release gates

- [x] Pinned official P1 testsuite without an environment-gated CI skip.
- [x] Generated P2 registration comparison against pinned official 0.2.0 WIT.
- [x] Differential P1 behavior against Wago, Wasmtime, and Wazero.
- [x] Blocked-I/O, slow-writer, cancellation, and cross-instance progress tests.
- [x] Read-only mount and child attenuation tests.
- [x] Resource exhaustion tests and ABI-decoder fuzz targets.
- [x] Linux amd64/arm64 and macOS arm64 jobs at the minimum and current Go versions.
- [x] Reproducible rebuild and pinned-toolchain checks for checked-in component fixtures.
- [x] Host-call allocation and parallel-instance benchmark coverage. Numeric
  promotion budgets remain a release-policy decision because they depend on the
  target Wago runtime and architecture.

## Remediation sequence

1. Correct ambient authority defaults and release metadata.
2. Refactor Preview 1 to explicit per-instance state and unified stdio descriptors.
3. Build shared real clock and pollable layers.
4. Correct Preview 2 descriptor, stream, polling, and quota semantics.
5. Generate the Preview 2 surface from pinned WIT.
6. Promote providers only after the mandatory release gates pass.

The implementation checklist is kept in the repository so future metadata
changes and releases can be reviewed against unresolved items.
