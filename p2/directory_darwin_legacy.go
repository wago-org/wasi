//go:build darwin && !go1.27

package p2

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	sysunix "golang.org/x/sys/unix"
)

// Darwin's public Getdirentries Go wrapper reopens and replays the directory
// on every refill. Use the native descriptor cursor instead. This kernel ABI
// is present on supported macOS versions; native CI must exercise this path.
// No private libc symbol or Go runtime interface is imported.
func readDarwinDirent(fd int, buffer []byte) (int, error) {
	var position int64 // Darwin off_t is 64 bits on both supported architectures.
	n, _, errno := syscall.Syscall6(sysunix.SYS_GETDIRENTRIES64,
		uintptr(fd), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)),
		uintptr(unsafe.Pointer(&position)), 0, 0)
	if errno != 0 {
		return 0, errno
	}
	return int(n), nil
}

func readDirectoryEntry(d *directoryStream, maxNameBytes uint64) (string, uint32, error) {
	// The stream mutex serializes resource close with these native fd calls.
	defer runtime.KeepAlive(d.file)
	fd := d.file.Fd()
	if fd == ^uintptr(0) {
		return "", 0, &os.PathError{Op: "getdirentries64", Path: d.file.Name(), Err: os.ErrClosed}
	}
	r := &d.reader
	for {
		if r.pos == r.end {
			if r.buffer == nil {
				r.buffer = directoryBuffers.Get().(*[8192]byte)
			}
			n, err := readDarwinDirent(int(fd), r.buffer[:])
			if errors.Is(err, sysunix.EINTR) {
				continue
			}
			if err != nil {
				return "", 0, err
			}
			if n == 0 {
				r.releaseBuffer()
				return "", 0, io.EOF
			}
			if n < 0 || n > len(r.buffer) {
				return "", 0, sysunix.EIO
			}
			r.pos, r.end = 0, n
		}
		nameBytes, typ, consumed, err := darwinDirectoryRecord(r.buffer[r.pos:r.end])
		if err != nil {
			return "", 0, err
		}
		r.pos += consumed
		if len(nameBytes) == 0 || bytes.Equal(nameBytes, []byte(".")) || bytes.Equal(nameBytes, []byte("..")) {
			continue
		}
		if uint64(len(nameBytes)) > maxNameBytes {
			return "", 0, hostFS.ENAMETOOLONG
		}
		name := string(nameBytes)
		if kind, known := nativeDirectoryType(typ); known {
			return name, kind, nil
		}
		// Go 1.22's ReadDir uses file.Name() here. Resolve unknown types through
		// the pinned descriptor instead, without following the entry's symlink.
		var stat sysunix.Stat_t
		if err := statDirectoryEntry(int(fd), name, &stat); err != nil {
			if errors.Is(err, sysunix.ENOENT) {
				continue // An entry removed during enumeration is no longer present.
			}
			return "", 0, err
		}
		return name, directoryStatKind(uint32(stat.Mode)), nil
	}
}

// Validate native fields before slicing: the last record may be shorter than
// the generated unix.Dirent, and name length excludes its terminating NUL.
func darwinDirectoryRecord(record []byte) (name []byte, typ uint8, consumed int, err error) {
	const (
		nameOffset   = int(unsafe.Offsetof(sysunix.Dirent{}.Name))
		reclenOffset = int(unsafe.Offsetof(sysunix.Dirent{}.Reclen))
		namlenOffset = int(unsafe.Offsetof(sysunix.Dirent{}.Namlen))
		typeOffset   = int(unsafe.Offsetof(sysunix.Dirent{}.Type))
		inoOffset    = int(unsafe.Offsetof(sysunix.Dirent{}.Ino))
	)
	if len(record) <= nameOffset {
		return nil, 0, 0, sysunix.EIO
	}
	consumed = int(binary.NativeEndian.Uint16(record[reclenOffset : reclenOffset+2]))
	if consumed <= nameOffset || consumed > len(record) {
		return nil, 0, 0, sysunix.EIO
	}
	length := int(binary.NativeEndian.Uint16(record[namlenOffset : namlenOffset+2]))
	if length >= consumed-nameOffset || record[nameOffset+length] != 0 {
		return nil, 0, 0, sysunix.EIO
	}
	name = record[nameOffset : nameOffset+length]
	if bytes.IndexByte(name, 0) >= 0 {
		return nil, 0, 0, sysunix.EIO
	}
	// Darwin marks removed directory entries with inode zero.
	if binary.NativeEndian.Uint64(record[inoOffset:inoOffset+8]) == 0 {
		return nil, 0, consumed, nil
	}
	return name, record[typeOffset], consumed, nil
}
