//go:build windows

package p2

import (
	"io/fs"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func appendTargetForFile(f *os.File, _ fs.FileInfo) (*appendTarget, error) {
	// FileInfo.Sys only exposes Windows attributes, not file identity. Query
	// the pinned handle once at descriptor creation, keeping the append hot
	// path unchanged. FILE_ID_INFO includes the full ReFS identifier.
	handle := windows.Handle(f.Fd())
	target, err := appendTargetForWindowsHandle(handle, queryWindowsAppendID, queryWindowsAppendVolume)
	runtime.KeepAlive(f)
	return target, err
}

type windowsAppendID struct {
	volume uint64
	id     [2]uint64
}

func queryWindowsAppendID(handle windows.Handle) (windowsAppendID, error) {
	var info windowsAppendID
	err := windows.GetFileInformationByHandleEx(handle, windows.FileIdInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	return info, err
}

func queryWindowsAppendVolume(handle windows.Handle) (uint32, error) {
	var info windows.ByHandleFileInformation
	err := windows.GetFileInformationByHandle(handle, &info)
	return info.VolumeSerialNumber, err
}

// Query results are passed by value so injectable driver controls need no
// process-wide hooks and production query buffers remain on the stack.
func appendTargetForWindowsHandle(handle windows.Handle, queryID func(windows.Handle) (windowsAppendID, error), queryVolume func(windows.Handle) (uint32, error)) (*appendTarget, error) {
	info, err := queryID(handle)
	if err == nil {
		// All bits participate; collisions only serialize unrelated files.
		return appendTargetForIdentity(info.volume, info.id[0]^info.id[1]*0x94d049bb133111eb), nil
	}
	switch err {
	case windows.ERROR_INVALID_PARAMETER, windows.ERROR_NOT_SUPPORTED, windows.ERROR_INVALID_FUNCTION, windows.ERROR_INVALID_LEVEL:
	default:
		return nil, err
	}
	// Drivers without FILE_ID_INFO use a volume-wide stripe. Legacy file IDs
	// can change on FAT rename, so using them could split aliases across locks.
	volume, err := queryVolume(handle)
	if err != nil {
		return nil, err
	}
	return appendTargetForIdentity(uint64(volume), 0), nil
}
