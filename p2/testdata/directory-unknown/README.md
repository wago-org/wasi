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

After testing, unmount with `fusermount3 -u "$fixture/mount"` and stop the
foreground fixture process. The test creates and removes only its own
temporary subtree.
