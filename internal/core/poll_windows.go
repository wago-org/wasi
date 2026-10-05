//go:build windows

package core

import (
	"context"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                       = windows.NewLazySystemDLL("kernel32.dll")
	ntdll                          = windows.NewLazySystemDLL("ntdll.dll")
	procPeekNamedPipe              = kernel32.NewProc("PeekNamedPipe")
	procGetNumberConsoleInputEvent = kernel32.NewProc("GetNumberOfConsoleInputEvents")
	procGetConsoleMode             = kernel32.NewProc("GetConsoleMode")
	procNtQueryInformationFile     = ntdll.NewProc("NtQueryInformationFile")
)

type pipeLocalInformation struct {
	NamedPipeType       uint32
	NamedPipeConfig     uint32
	MaximumInstances    uint32
	CurrentInstances    uint32
	InboundQuota        uint32
	ReadDataAvailable   uint32
	OutboundQuota       uint32
	WriteQuotaAvailable uint32
	NamedPipeState      uint32
	NamedPipeEnd        uint32
}

const (
	filePipeLocalInformation = 24
	pipeDisconnected         = 1
	pipeClosing              = 4
)

func queryPipe(handle windows.Handle) (pipeLocalInformation, bool) {
	var status windows.IO_STATUS_BLOCK
	var info pipeLocalInformation
	result, _, _ := procNtQueryInformationFile.Call(uintptr(handle), uintptr(unsafe.Pointer(&status)),
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), filePipeLocalInformation)
	return info, windows.NTStatus(result) == 0
}

func osFileReady(file *os.File, typ byte) bool {
	conn, err := file.SyscallConn()
	if err != nil {
		return false
	}
	var ready bool
	_ = conn.Control(func(raw uintptr) {
		info, statErr := file.Stat()
		if statErr == nil && info.Mode().IsRegular() {
			ready = true
			return
		}
		handle := windows.Handle(raw)
		if typ == 2 {
			if pipe, ok := queryPipe(handle); ok {
				ready = pipe.NamedPipeState == pipeDisconnected || pipe.NamedPipeState == pipeClosing || pipe.WriteQuotaAvailable != 0
				return
			}
			var mode uint32
			if ok, _, _ := procGetConsoleMode.Call(uintptr(handle), uintptr(unsafe.Pointer(&mode))); ok != 0 {
				ready = true
			}
			return
		}
		var available uint32
		if ok, _, callErr := procPeekNamedPipe.Call(uintptr(handle), 0, 0, 0, uintptr(unsafe.Pointer(&available)), 0); ok != 0 {
			ready = available != 0
			return
		} else if callErr == windows.ERROR_BROKEN_PIPE || callErr == windows.ERROR_PIPE_NOT_CONNECTED {
			ready = true
			return
		}
		var events uint32
		if ok, _, _ := procGetNumberConsoleInputEvent.Call(uintptr(handle), uintptr(unsafe.Pointer(&events))); ok != 0 {
			ready = events != 0
		}
	})
	return ready
}

func osFileError(file *os.File) uint16 {
	conn, err := file.SyscallConn()
	if err != nil {
		return wasiEBadf
	}
	var statusErr error
	if err := conn.Control(func(raw uintptr) {
		_, statusErr = windows.GetFileType(windows.Handle(raw))
	}); err != nil {
		return wasiEBadf
	}
	return uint16(errno(statusErr))
}

func waitOSFiles(ctx context.Context, files []pollFile) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, file := range files {
			if osFileError(file.file) != 0 || osFileReady(file.file, file.typ) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
