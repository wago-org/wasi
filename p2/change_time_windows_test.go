//go:build windows

package p2

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsRawFileInfoDoesNotClaimCreationAsChange(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "file"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, change, _, _ := hostStat(info)
	if !change.IsZero() {
		t.Fatalf("raw FileInfo exposed creation time %v as status-change time", change)
	}
}

func TestWindowsStatUsesNativeStatusChangeTime(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "file"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var native struct {
		CreationTime, LastAccessTime, LastWriteTime, ChangeTime int64
		Attributes                                              uint32
		_                                                       uint32
	}
	if err := windows.GetFileInformationByHandleEx(windows.Handle(f.Fd()), windows.FileBasicInfo,
		(*byte)(unsafe.Pointer(&native)), uint32(unsafe.Sizeof(native))); err != nil {
		t.Fatal(err)
	}
	info, err := statFile(f)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, got, _, _ := hostStat(info)
	want := filetimeTime(syscall.Filetime{LowDateTime: uint32(native.ChangeTime), HighDateTime: uint32(uint64(native.ChangeTime) >> 32)})
	if !got.Equal(want) {
		t.Fatalf("status-change time = %v, want native %v", got, want)
	}
}
