//go:build linux

package p2

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"runtime"
	"unsafe"

	sysunix "golang.org/x/sys/unix"
)

func readDirectoryEntry(d *directoryStream, maxNameBytes uint64) (string, uint32, error) {
	// The stream mutex serializes resource close with these native fd calls.
	defer runtime.KeepAlive(d.file)
	fd := d.file.Fd()
	if fd == ^uintptr(0) {
		return "", 0, &os.PathError{Op: "readdirent", Path: d.file.Name(), Err: os.ErrClosed}
	}
	r := &d.reader
	for {
		if r.pos == r.end {
			if r.buffer == nil {
				r.buffer = directoryBuffers.Get().(*[8192]byte)
			}
			n, err := sysunix.ReadDirent(int(fd), r.buffer[:])
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
		nameBytes, typ, consumed, err := linuxDirectoryRecord(r.buffer[r.pos:r.end])
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

// Read only validated field slices; the final record can be shorter than the
// generated unix.Dirent structure, so casting it to that structure is unsafe.
func linuxDirectoryRecord(record []byte) (name []byte, typ uint8, consumed int, err error) {
	const (
		nameOffset   = int(unsafe.Offsetof(sysunix.Dirent{}.Name))
		reclenOffset = int(unsafe.Offsetof(sysunix.Dirent{}.Reclen))
		typeOffset   = int(unsafe.Offsetof(sysunix.Dirent{}.Type))
	)
	if len(record) <= nameOffset {
		return nil, 0, 0, sysunix.EIO
	}
	consumed = int(binary.NativeEndian.Uint16(record[reclenOffset : reclenOffset+2]))
	if consumed <= nameOffset || consumed > len(record) {
		return nil, 0, 0, sysunix.EIO
	}
	name = record[nameOffset:consumed]
	end := bytes.IndexByte(name, 0)
	if end < 0 {
		return nil, 0, 0, sysunix.EIO
	}
	return name[:end], record[typeOffset], consumed, nil
}
