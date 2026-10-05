//go:build darwin

package core

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestAllocateFileAtNonzeroOffset(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "allocate")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	err = allocateFile(f, 4096, 4096)
	if errors.Is(err, unix.EOPNOTSUPP) {
		t.Skip("filesystem does not support preallocation")
	}
	if err != nil {
		t.Fatalf("allocateFile with nonzero offset: %v", err)
	}
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() < 8192 {
		t.Fatalf("size after allocation = %d, want at least 8192", info.Size())
	}
}

func allocateStat(t *testing.T, file *os.File) unix.Stat_t {
	t.Helper()
	var st unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func allocateSupported(t *testing.T, file *os.File, offset, length int64) {
	t.Helper()
	if err := allocateFile(file, offset, length); err != nil {
		if errors.Is(err, unix.EOPNOTSUPP) {
			t.Skip("filesystem does not support preallocation")
		}
		t.Fatalf("allocateFile(%d, %d): %v", offset, length, err)
	}
}

// A repeat over newly allocated zero bytes may be reported as a logical hole
// even when physically allocated. Rejection is conservative, and must not grow
// the reservation or change the file.
func allocateRepeat(t *testing.T, file *os.File, offset, length int64) {
	t.Helper()
	if err := allocateFile(file, offset, length); err != nil && !errors.Is(err, unix.EOPNOTSUPP) {
		t.Fatalf("repeat allocateFile(%d, %d): %v", offset, length, err)
	}
}

func TestAllocateFileReusesPhysicalCapacity(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "allocate")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	// A dense file exposes the difference between target length and additional
	// bytes. Keep this small enough to run without requiring a disk image.
	const size = 256 << 10
	data := bytes.Repeat([]byte{0x5a}, size)
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	before := allocateStat(t, file)
	if before.Blocks*512 < size {
		t.Skip("filesystem stores the test data sparsely or compressed")
	}
	allocateSupported(t, file, size, 4096)
	after := allocateStat(t, file)
	if after.Size != size+4096 {
		t.Fatalf("size = %d, want %d", after.Size, size+4096)
	}
	if extra := (after.Blocks - before.Blocks) * 512; extra > 4096+(64<<10) {
		t.Fatalf("allocation grew by %d bytes for a 4096-byte extension", extra)
	}
	for i := 0; i < 8; i++ {
		allocateRepeat(t, file, size, 4096)
		allocateSupported(t, file, 4096, 4096)
	}
	repeated := allocateStat(t, file)
	if repeated.Blocks != after.Blocks {
		t.Fatalf("repeated allocation grew from %d to %d blocks", after.Blocks, repeated.Blocks)
	}
	if repeated.Size != size+4096 {
		t.Fatalf("repeated allocation changed size to %d", repeated.Size)
	}
	contents := make([]byte, size+4096)
	if _, err := file.ReadAt(contents, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(contents[:size], data) || !bytes.Equal(contents[size:], make([]byte, 4096)) {
		t.Fatal("allocation changed existing content or did not zero the extension")
	}
	if offset, err := file.Seek(0, io.SeekCurrent); err != nil || offset != size {
		t.Fatalf("file offset = %d, %v; want %d", offset, err, size)
	}
}

func TestAllocateFileSparseExtensionAndInterior(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "allocate")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	const size = 128 << 10
	if _, err := file.WriteAt([]byte("sentinel"), size-8); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	before := allocateStat(t, file)
	if before.Blocks*512 < before.Size {
		if err := allocateFile(file, 0, 4096); !errors.Is(err, unix.EOPNOTSUPP) {
			t.Fatalf("sparse interior allocation = %v, want EOPNOTSUPP", err)
		}
		if after := allocateStat(t, file); after.Size != before.Size || after.Blocks != before.Blocks {
			t.Fatal("unsupported sparse interior allocation modified the file")
		}
	}
	allocateSupported(t, file, size, 4096)
	after := allocateStat(t, file)
	if after.Size != size+4096 || after.Blocks*512 < size+4096 {
		t.Fatalf("sparse extension: size %d, allocated %d; want %d", after.Size, after.Blocks*512, size+4096)
	}
	allocateRepeat(t, file, size, 4096)
	if repeated := allocateStat(t, file); repeated.Blocks != after.Blocks {
		t.Fatalf("repeated sparse extension grew from %d to %d blocks", after.Blocks, repeated.Blocks)
	}
	contents := make([]byte, size+4096)
	if _, err := file.ReadAt(contents, 0); err != nil {
		t.Fatal(err)
	}
	if string(contents[size-8:size]) != "sentinel" || !bytes.Equal(contents[:size-8], make([]byte, size-8)) || !bytes.Equal(contents[size:], make([]byte, 4096)) {
		t.Fatal("sparse allocation changed contents")
	}
}

func TestAllocateFilePreservesReadonlyErrorAndChecksOverflow(t *testing.T) {
	path := t.TempDir() + "/allocate"
	if err := os.WriteFile(path, bytes.Repeat([]byte{0x5a}, 8192), 0600); err != nil {
		t.Fatal(err)
	}
	readonly, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer readonly.Close()
	if err := allocateFile(readonly, 0, 4096); !errors.Is(err, unix.EBADF) {
		t.Fatalf("readonly allocation = %v, want EBADF", err)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, request := range [][2]int64{{-1, 1}, {0, -1}, {int64(maxInt64Value), 1}} {
		if err := allocateFile(file, request[0], request[1]); !errors.Is(err, unix.EINVAL) {
			t.Fatalf("allocateFile(%d, %d) = %v, want EINVAL", request[0], request[1], err)
		}
	}
}

func TestAllocateFileAtZeroOffset(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "allocate")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	allocateSupported(t, file, 0, 8192)
	before := allocateStat(t, file)
	if before.Size != 8192 || before.Blocks*512 < 8192 {
		t.Fatalf("zero-offset allocation: size %d, allocated %d", before.Size, before.Blocks*512)
	}
	allocateRepeat(t, file, 0, 8192)
	if after := allocateStat(t, file); after.Size != before.Size || after.Blocks != before.Blocks {
		t.Fatal("repeated zero-offset allocation changed size or capacity")
	}
	contents := make([]byte, 8192)
	if _, err := file.ReadAt(contents, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(contents, make([]byte, 8192)) {
		t.Fatal("newly allocated bytes are not zero")
	}
}

func TestAllocateFileInteriorHoleWithReservationBeyondEOF(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "allocate")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	const size = 128 << 10
	if _, err := file.WriteAt([]byte("sentinel"), size-8); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	// A native reservation does not extend logical EOF and can make aggregate
	// block counts larger than size while an interior hole remains.
	store := unix.Fstore_t{Flags: unix.F_ALLOCATEALL, Posmode: unix.F_PEOFPOSMODE, Length: size * 4}
	if err := unix.FcntlFstore(file.Fd(), unix.F_PREALLOCATE, &store); err != nil {
		if errors.Is(err, unix.EOPNOTSUPP) {
			t.Skip("filesystem does not support preallocation")
		}
		t.Fatal(err)
	}
	before := allocateStat(t, file)
	if before.Size != size || before.Blocks*512 < size {
		t.Fatalf("native reservation: size %d, allocated %d", before.Size, before.Blocks*512)
	}
	hole, err := file.Seek(0, unix.SEEK_HOLE)
	if err != nil {
		t.Skipf("filesystem cannot report holes: %v", err)
	}
	if hole >= 4096 {
		t.Skip("native reservation backed the interior range")
	}
	if _, err := file.Seek(47, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if err := allocateFile(file, 0, 4096); !errors.Is(err, unix.EOPNOTSUPP) {
		t.Fatalf("reserved sparse interior allocation = %v, want EOPNOTSUPP", err)
	}
	if after := allocateStat(t, file); after.Size != before.Size || after.Blocks != before.Blocks {
		t.Fatal("rejected interior allocation modified size or capacity")
	}
	if position, err := file.Seek(0, io.SeekCurrent); err != nil || position != 47 {
		t.Fatalf("file offset after rejection = %d, %v; want 47", position, err)
	}
	contents := make([]byte, size)
	if _, err := file.ReadAt(contents, 0); err != nil {
		t.Fatal(err)
	}
	if string(contents[size-8:]) != "sentinel" || !bytes.Equal(contents[:size-8], make([]byte, size-8)) {
		t.Fatal("rejected interior allocation changed contents")
	}
}
