//go:build windows

package winfs

import (
	"encoding/binary"
	"errors"
	"syscall"
	"testing"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

func symlinkTargetData(target, display string, flags uint32) []byte {
	printUnits := utf16.Encode([]rune(display))
	targetUnits := utf16.Encode([]rune(target))
	data := make([]byte, 20+2*(len(printUnits)+len(targetUnits)))
	binary.LittleEndian.PutUint32(data, windows.IO_REPARSE_TAG_SYMLINK)
	binary.LittleEndian.PutUint16(data[4:], uint16(len(data)-8))
	binary.LittleEndian.PutUint16(data[8:], uint16(2*len(printUnits)))
	binary.LittleEndian.PutUint16(data[10:], uint16(2*len(targetUnits)))
	binary.LittleEndian.PutUint16(data[14:], uint16(2*len(printUnits)))
	binary.LittleEndian.PutUint32(data[16:], flags)
	for i, unit := range append(printUnits, targetUnits...) {
		binary.LittleEndian.PutUint16(data[20+i*2:], unit)
	}
	return data
}

func TestParseSymlinkTargetUsesSubstituteNameAndRelativeFlag(t *testing.T) {
	for _, tc := range []struct {
		name, target, display string
		flags                 uint32
		relative              bool
	}{
		{"relative", `directory\file`, "display label", symlinkFlagRelative, true},
		{"no display", "file", "", symlinkFlagRelative, true},
		{"unicode", "directory/📄", "label", symlinkFlagRelative, true},
		{"absolute flag", `C:\directory\file`, "label", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, relative, err := parseSymlinkTarget(symlinkTargetData(tc.target, tc.display, tc.flags))
			if err != nil || target != tc.target || relative != tc.relative {
				t.Fatalf("target = %q, relative = %v, error = %v", target, relative, err)
			}
		})
	}
}

func TestParseSymlinkTargetRejectsMalformedData(t *testing.T) {
	valid := symlinkTargetData("file", "", symlinkFlagRelative)
	for _, tc := range []struct {
		name string
		edit func([]byte) []byte
	}{
		{"short header", func(b []byte) []byte { return b[:19] }},
		{"different tag", func(b []byte) []byte { binary.LittleEndian.PutUint32(b, 0); return b }},
		{"short payload", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[4:], 11); return b }},
		{"truncated payload", func(b []byte) []byte { return b[:len(b)-1] }},
		{"empty target", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[10:], 0); return b }},
		{"odd offset", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[8:], 1); return b }},
		{"odd length", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[10:], 3); return b }},
		{"offset beyond payload", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[8:], 0xfffe); return b }},
		{"length beyond payload", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[10:], 0xfffe); return b }},
		{"outside declared payload", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[4:], 12); return b }},
		{"unknown flags", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[16:], 2); return b }},
		{"nul in target", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[20:], 0); return b }},
		{"unpaired high surrogate", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[20:], 0xd800); return b }},
		{"unpaired low surrogate", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[20:], 0xdc00); return b }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.edit(append([]byte(nil), valid...))
			if _, _, err := parseSymlinkTarget(raw); !errors.Is(err, syscall.EINVAL) {
				t.Fatalf("malformed target error = %v, want EINVAL", err)
			}
		})
	}
}

func BenchmarkParseSymlinkTarget(b *testing.B) {
	for _, target := range []string{`directory\file`, "資料/📄"} {
		b.Run(target, func(b *testing.B) {
			raw := symlinkTargetData(target, "label", symlinkFlagRelative)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := parseSymlinkTarget(raw); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
