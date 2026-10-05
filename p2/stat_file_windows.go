//go:build windows

package p2

import (
	"io/fs"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsFileStat struct {
	fs.FileInfo
	links      uint64
	changeTime uint64
}

type windowsBasicInfo struct {
	CreationTime, LastAccessTime, LastWriteTime, ChangeTime int64
	Attributes                                              uint32
	_                                                       uint32
}

func statFile(f *os.File) (fs.FileInfo, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var native windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &native)
	if err != nil {
		runtime.KeepAlive(f)
		return nil, err
	}
	var basic windowsBasicInfo
	// Some filesystems do not expose ChangeTime. In that case report the
	// optional WASI timestamp as unavailable rather than substituting birth time.
	if err := windows.GetFileInformationByHandleEx(windows.Handle(f.Fd()), windows.FileBasicInfo,
		(*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic))); err != nil {
		basic.ChangeTime = 0
	}
	runtime.KeepAlive(f)
	return windowsFileStat{FileInfo: info, links: uint64(native.NumberOfLinks), changeTime: uint64(basic.ChangeTime)}, nil
}
