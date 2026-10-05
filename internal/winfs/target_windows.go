//go:build windows

package winfs

import (
	"encoding/binary"
	"os"
	"strings"
	"syscall"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

// ReadlinkTargetAt reads the operative target and relative flag of a symbolic
// link. ReadlinkAt separately preserves the link's display name.
func ReadlinkTargetAt(root windows.Handle, name string) (target string, relative bool, err error) {
	h, err := OpenAt(root, name, os.O_RDONLY, 0, false, false, true)
	if err != nil {
		return "", false, err
	}
	defer windows.CloseHandle(h)
	raw := make([]byte, windows.MAXIMUM_REPARSE_DATA_BUFFER_SIZE)
	var returned uint32
	if err := windows.DeviceIoControl(h, windows.FSCTL_GET_REPARSE_POINT, nil, 0, &raw[0], uint32(len(raw)), &returned, nil); err != nil {
		return "", false, err
	}
	if returned > uint32(len(raw)) {
		return "", false, syscall.EINVAL
	}
	return parseSymlinkTarget(raw[:returned])
}

func parseSymlinkTarget(raw []byte) (target string, relative bool, err error) {
	const headerSize = 20
	if len(raw) < headerSize || binary.LittleEndian.Uint32(raw) != windows.IO_REPARSE_TAG_SYMLINK {
		return "", false, syscall.EINVAL
	}
	dataSize := int(binary.LittleEndian.Uint16(raw[4:]))
	if dataSize < headerSize-8 || dataSize > len(raw)-8 {
		return "", false, syscall.EINVAL
	}
	flags := binary.LittleEndian.Uint32(raw[16:])
	if flags&^uint32(symlinkFlagRelative) != 0 {
		return "", false, syscall.EINVAL
	}
	offset := int(binary.LittleEndian.Uint16(raw[8:]))
	size := int(binary.LittleEndian.Uint16(raw[10:]))
	if size == 0 || offset%2 != 0 || size%2 != 0 || offset+size > dataSize-(headerSize-8) {
		return "", false, syscall.EINVAL
	}
	name := raw[headerSize+offset : headerSize+offset+size]
	utf8Size := 0
	for i := 0; i < len(name); i += 2 {
		u := binary.LittleEndian.Uint16(name[i:])
		if u == 0 {
			return "", false, syscall.EINVAL
		}
		if u >= 0xd800 && u <= 0xdbff {
			if i+2 == len(name) {
				return "", false, syscall.EINVAL
			}
			low := binary.LittleEndian.Uint16(name[i+2:])
			if low < 0xdc00 || low > 0xdfff {
				return "", false, syscall.EINVAL
			}
			utf8Size += 4
			i += 2
		} else if u >= 0xdc00 && u <= 0xdfff {
			return "", false, syscall.EINVAL
		} else {
			utf8Size += utf8.RuneLen(rune(u))
		}
	}
	var decoded strings.Builder
	decoded.Grow(utf8Size)
	for i := 0; i < len(name); i += 2 {
		u := binary.LittleEndian.Uint16(name[i:])
		r := rune(u)
		if u >= 0xd800 && u <= 0xdbff {
			r = utf16.DecodeRune(r, rune(binary.LittleEndian.Uint16(name[i+2:])))
			i += 2
		}
		decoded.WriteRune(r)
	}
	return decoded.String(), flags&symlinkFlagRelative != 0, nil
}
