# Directory entries without native type metadata

`unknown.c` is a small passthrough FUSE fixture for the ordinary directory
rename/replacement regression. It enumerates a pinned host `DIR` and supplies
no entry attributes to the FUSE filler, producing `DT_UNKNOWN` records. It
uses the installed libfuse3 runtime and development headers; WASI has no new
runtime or module dependencies.

Compile and mount it in one terminal (the backing directory must be empty):

```sh
fixture=$(mktemp -d)
mkdir "$fixture/backing" "$fixture/mount"
cc -Wall -Wextra -O2 -D_FILE_OFFSET_BITS=64 \
  $(pkg-config fuse3 --cflags) p2/testdata/directory-unknown/unknown.c \
  $(pkg-config fuse3 --libs) -o "$fixture/unknown"
"$fixture/unknown" "$fixture/backing" "$fixture/mount" -f -s \
  -o attr_timeout=0,entry_timeout=0,negative_timeout=0
```

In another terminal, compile the test executable outside the mounted tree,
using Go 1.22 to exercise its pathname-based unknown-type fallback. Then set
the optional fixture root for only the directory tests/benchmark:

```sh
go test -c -o /tmp/wasi-directory.test ./p2
WASI_TEST_DIRECTORY_ROOT="$fixture/mount" /tmp/wasi-directory.test \
  -test.run '^TestDirectoryEntryStreamSurvivesDirectoryRename$' -test.v
WASI_TEST_DIRECTORY_ROOT="$fixture/mount" /tmp/wasi-directory.test \
  -test.run '^$' -test.bench '^BenchmarkReadDirectoryEntries$' -test.benchmem
```

The test opens a directory, renames it, and creates another directory at its
old path with opposite entry types. Results must retain the types from the
open descriptor. Without the descriptor-relative fix, Go 1.22 reports the
replacement directory's types; ordinary filesystems with native entry types
and newer Go versions can pass without reaching that fallback.

`BenchmarkReadDirectoryEntries` reports time, bytes and allocations for a
fresh stream reading 32 entries. Run it without the fixture for ordinary
native entry types and with the fixture for `DT_UNKNOWN`. Linux batches
native records in a pooled 8 KiB buffer and stats only unknown types through
`fstatat`. Darwin before Go 1.27 uses the kernel `getdirentries64` cursor
with the same bounded pool, preserving native entry types and statting only
unknown entries. It uses public Go `syscall.Syscall6` and the exported syscall
number, with no private libc or runtime imports. This relies on Darwin's
kernel syscall ABI, rather than a typed libc wrapper; native macOS Go 1.22
CI is required to validate permission, close, cursor and rename behavior.
Go 1.27+ Darwin retains `ReadDir`, whose unknown-type fallback is
descriptor-relative. Windows retains `ReadDir` and its independent handle.

The ordinary rename/replacement and entry-type tests run on native Darwin.
Legacy Darwin also tests truncated native records and a seeded unknown-type
record against an actual renamed descriptor. The read-only directory control
checks known-type enumeration with read permission and no search permission,
and confirms that `fstatat` would fail. The Linux fixture supplies the actual
old-Go unknown-type red reproduction. The checked-in benchmark measures
native and unknown-type paths without imposing one stat on each known entry.

After testing, unmount with `fusermount3 -u "$fixture/mount"` and stop the
foreground fixture process. The test creates and removes only its own
temporary subtree.
