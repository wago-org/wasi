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
	info, err := file.Stat()
	if err == nil && info.Mode().IsRegular() {
		return true
	}
	handle := windows.Handle(file.Fd())
	if typ == 2 {
		if pipe, ok := queryPipe(handle); ok {
			return pipe.NamedPipeState == pipeDisconnected || pipe.NamedPipeState == pipeClosing || pipe.WriteQuotaAvailable != 0
		}
		var mode uint32
		if ok, _, _ := procGetConsoleMode.Call(uintptr(handle), uintptr(unsafe.Pointer(&mode))); ok != 0 {
			return true
		}
		return false
	}
	var available uint32
	if ok, _, callErr := procPeekNamedPipe.Call(uintptr(handle), 0, 0, 0, uintptr(unsafe.Pointer(&available)), 0); ok != 0 {
		return available != 0
	} else if callErr == windows.ERROR_BROKEN_PIPE || callErr == windows.ERROR_PIPE_NOT_CONNECTED {
		return true
	}
	var events uint32
	if ok, _, _ := procGetNumberConsoleInputEvent.Call(uintptr(handle), uintptr(unsafe.Pointer(&events))); ok != 0 {
		return events != 0
	}
	return false
}

func waitOSFiles(ctx context.Context, files []pollFile) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, file := range files {
			if osFileReady(file.file, file.typ) {
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
