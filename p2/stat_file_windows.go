//go:build windows

package p2

import (
	"io/fs"
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

type windowsFileStat struct {
	fs.FileInfo
	links uint64
}

func statFile(f *os.File) (fs.FileInfo, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var native windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &native)
	runtime.KeepAlive(f)
	if err != nil {
		return nil, err
	}
	return windowsFileStat{FileInfo: info, links: uint64(native.NumberOfLinks)}, nil
}
