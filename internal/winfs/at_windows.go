//go:build windows

// Portions Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license.

// Package winfs provides handle-relative Windows filesystem operations.
package winfs

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	fileRenameInformation      = 10
	fileLinkInformation        = 11
	fileDispositionInformation = 13
	fileDispositionInfoEx      = 64
	symlinkFlagRelative        = 1
)

func objectAttributes(root windows.Handle, name string, noReparse bool) (*windows.OBJECT_ATTRIBUTES, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, err
	}
	attrs := uint32(windows.OBJ_CASE_INSENSITIVE)
	if noReparse {
		attrs |= windows.OBJ_DONT_REPARSE
	}
	return &windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: root,
		ObjectName:    objectName,
		Attributes:    attrs,
	}, nil
}

func errno(err error) error {
	if status, ok := err.(windows.NTStatus); ok {
		switch status {
		case windows.STATUS_REPARSE_POINT_ENCOUNTERED:
			return syscall.ELOOP
		case windows.STATUS_NOT_A_DIRECTORY:
			return syscall.ENOTDIR
		case windows.STATUS_FILE_IS_A_DIRECTORY:
			return syscall.EISDIR
		case windows.STATUS_OBJECT_NAME_COLLISION:
			return syscall.EEXIST
		}
		return status.Errno()
	}
	return err
}

// OpenAt opens name relative to root. noReparse rejects reparse points in every
// path component; noFollow opens a reparse-point leaf itself.
func OpenAt(root windows.Handle, name string, flags int, mode uint32, directory, noReparse, noFollow bool) (windows.Handle, error) {
	return openAt(root, name, flags, mode, directory, noReparse, noFollow, 0)
}

// OpenAtAccess is OpenAt with additional native access rights.
func OpenAtAccess(root windows.Handle, name string, flags int, mode uint32, directory, noReparse, noFollow bool, additionalAccess uint32) (windows.Handle, error) {
	return openAt(root, name, flags, mode, directory, noReparse, noFollow, additionalAccess)
}

func openAt(root windows.Handle, name string, flags int, mode uint32, directory, noReparse, noFollow bool, additionalAccess uint32) (windows.Handle, error) {
	if name == "" {
		return windows.InvalidHandle, windows.ERROR_FILE_NOT_FOUND
	}
	if name == "." {
		if flags&os.O_CREATE != 0 && flags&os.O_EXCL != 0 {
			return windows.InvalidHandle, syscall.EEXIST
		}
		if flags&(os.O_CREATE|os.O_TRUNC|os.O_WRONLY|os.O_RDWR) != 0 {
			return windows.InvalidHandle, syscall.EISDIR
		}
		process := windows.CurrentProcess()
		var duplicate windows.Handle
		if err := windows.DuplicateHandle(process, root, process, &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
			return windows.InvalidHandle, err
		}
		return duplicate, nil
	}
	oa, err := objectAttributes(root, name, noReparse)
	if err != nil {
		return windows.InvalidHandle, err
	}
	access := uint32(windows.FILE_GENERIC_READ)
	switch flags & (os.O_WRONLY | os.O_RDWR) {
	case os.O_WRONLY:
		access = windows.FILE_GENERIC_WRITE
	case os.O_RDWR:
		access = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE
	}
	if flags&os.O_CREATE != 0 {
		access |= windows.FILE_GENERIC_WRITE
	}
	if flags&os.O_APPEND != 0 {
		access |= windows.FILE_APPEND_DATA
		if flags&os.O_TRUNC == 0 {
			access &^= windows.FILE_WRITE_DATA
		}
	}
	access |= windows.STANDARD_RIGHTS_READ | windows.FILE_READ_ATTRIBUTES | windows.FILE_READ_EA | windows.SYNCHRONIZE
	access |= additionalAccess

	disposition := uint32(windows.FILE_OPEN)
	if flags&os.O_CREATE != 0 && flags&os.O_EXCL != 0 {
		disposition = windows.FILE_CREATE
		noFollow = true
	} else if flags&os.O_CREATE != 0 {
		disposition = windows.FILE_OPEN_IF
	}
	options := uint32(windows.FILE_OPEN_FOR_BACKUP_INTENT | windows.FILE_SYNCHRONOUS_IO_NONALERT)
	if directory {
		options |= windows.FILE_DIRECTORY_FILE
	}
	if noFollow {
		options |= windows.FILE_OPEN_REPARSE_POINT
	}
	attrs := uint32(windows.FILE_ATTRIBUTE_NORMAL)
	if mode&0o200 == 0 {
		attrs = windows.FILE_ATTRIBUTE_READONLY
	}
	var handle windows.Handle
	err = windows.NtCreateFile(&handle, access, oa, &windows.IO_STATUS_BLOCK{}, nil, attrs,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		disposition, options, 0, 0)
	if err != nil {
		return windows.InvalidHandle, errno(err)
	}
	return handle, nil
}

func MkdirAt(root windows.Handle, name string) error {
	h, err := OpenAt(root, name, os.O_RDONLY|os.O_CREATE|os.O_EXCL, 0o777, true, true, true)
	if err != nil {
		return err
	}
	return windows.CloseHandle(h)
}

func openForMutation(root windows.Handle, name string, directory int, access uint32) (windows.Handle, error) {
	oa, err := objectAttributes(root, name, false)
	if err != nil {
		return windows.InvalidHandle, err
	}
	options := uint32(windows.FILE_OPEN_REPARSE_POINT | windows.FILE_OPEN_FOR_BACKUP_INTENT | windows.FILE_SYNCHRONOUS_IO_NONALERT)
	if directory > 0 {
		options |= windows.FILE_DIRECTORY_FILE
	} else if directory == 0 {
		options |= windows.FILE_NON_DIRECTORY_FILE
	}
	var handle windows.Handle
	err = windows.NtCreateFile(&handle, access|windows.SYNCHRONIZE, oa, &windows.IO_STATUS_BLOCK{}, nil, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN, options, 0, 0)
	if err != nil {
		return windows.InvalidHandle, errno(err)
	}
	return handle, nil
}

func DeleteAt(root windows.Handle, name string, directory bool) error {
	if name == "." {
		return syscall.EINVAL
	}
	h, err := openForMutation(root, name, -1, windows.DELETE|windows.FILE_READ_ATTRIBUTES)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(h), name)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	isSymlink := info.Mode()&os.ModeSymlink != 0
	if directory && (!info.IsDir() || isSymlink) {
		return syscall.ENOTDIR
	}
	if !directory && info.IsDir() && !isSymlink {
		return syscall.EISDIR
	}
	flags := uint32(windows.FILE_DISPOSITION_DELETE | windows.FILE_DISPOSITION_POSIX_SEMANTICS |
		windows.FILE_DISPOSITION_FORCE_IMAGE_SECTION_CHECK | windows.FILE_DISPOSITION_IGNORE_READONLY_ATTRIBUTE)
	err = windows.NtSetInformationFile(h, &windows.IO_STATUS_BLOCK{}, (*byte)(unsafe.Pointer(&flags)), uint32(unsafe.Sizeof(flags)), fileDispositionInfoEx)
	if err == nil {
		return nil
	}
	if status, ok := err.(windows.NTStatus); !ok || status != windows.STATUS_INVALID_INFO_CLASS && status != windows.STATUS_INVALID_PARAMETER && status != windows.STATUS_NOT_SUPPORTED {
		return errno(err)
	}
	deleteFile := byte(1)
	err = windows.NtSetInformationFile(h, &windows.IO_STATUS_BLOCK{}, &deleteFile, 1, fileDispositionInformation)
	return errno(err)
}

type renameInformation struct {
	ReplaceIfExists byte
	_               [7]byte
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [windows.MAX_PATH]uint16
}

type linkInformation struct {
	ReplaceIfExists byte
	_               [7]byte
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [windows.MAX_PATH]uint16
}

func RenameAt(oldRoot windows.Handle, oldName string, newRoot windows.Handle, newName string) error {
	h, err := openForMutation(oldRoot, oldName, -1, windows.DELETE)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	name, err := windows.UTF16FromString(newName)
	if err != nil || len(name) > windows.MAX_PATH {
		return syscall.EINVAL
	}
	info := renameInformation{ReplaceIfExists: 1, RootDirectory: newRoot, FileNameLength: uint32((len(name) - 1) * 2)}
	copy(info.FileName[:], name)
	err = windows.NtSetInformationFile(h, &windows.IO_STATUS_BLOCK{}, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), fileRenameInformation)
	return errno(err)
}

func LinkAt(oldRoot windows.Handle, oldName string, newRoot windows.Handle, newName string) error {
	h, err := openForMutation(oldRoot, oldName, 0, windows.FILE_WRITE_ATTRIBUTES)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	name, err := windows.UTF16FromString(newName)
	if err != nil || len(name) > windows.MAX_PATH {
		return syscall.EINVAL
	}
	info := linkInformation{RootDirectory: newRoot, FileNameLength: uint32((len(name) - 1) * 2)}
	copy(info.FileName[:], name)
	err = windows.NtSetInformationFile(h, &windows.IO_STATUS_BLOCK{}, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), fileLinkInformation)
	return errno(err)
}

func TargetIsDirectory(root windows.Handle, target string) bool {
	converted := filepath.FromSlash(target)
	clean := filepath.Clean(converted)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return false
	}
	h, err := OpenAt(root, clean, os.O_RDONLY, 0, true, true, false)
	if err != nil {
		return false
	}
	_ = windows.CloseHandle(h)
	return true
}

type reparseDataBuffer struct {
	ReparseTag        uint32
	ReparseDataLength uint16
	Reserved          uint16
	SubstituteOffset  uint16
	SubstituteLength  uint16
	PrintOffset       uint16
	PrintLength       uint16
	Flags             uint32
}

func SymlinkAt(target string, root windows.Handle, name string, directory bool) error {
	if target == "" {
		return syscall.EINVAL
	}
	return withSymlinkPrivilege(func() error {
		return symlinkAt(target, root, name, directory)
	})
}

func symlinkAt(target string, root windows.Handle, name string, directory bool) error {
	substitute := filepath.FromSlash(target)
	substitute16, err := windows.UTF16FromString(substitute)
	if err != nil {
		return err
	}
	substitute16 = substitute16[:len(substitute16)-1]
	print16, err := windows.UTF16FromString(target)
	if err != nil {
		return err
	}
	print16 = print16[:len(print16)-1]
	options := uint32(windows.FILE_OPEN_REPARSE_POINT | windows.FILE_OPEN_FOR_BACKUP_INTENT | windows.FILE_SYNCHRONOUS_IO_NONALERT)
	if directory {
		options |= windows.FILE_DIRECTORY_FILE
	} else {
		options |= windows.FILE_NON_DIRECTORY_FILE
	}
	oa, err := objectAttributes(root, name, false)
	if err != nil {
		return err
	}
	var handle windows.Handle
	err = windows.NtCreateFile(&handle, windows.SYNCHRONIZE|windows.FILE_WRITE_ATTRIBUTES|windows.DELETE,
		oa, &windows.IO_STATUS_BLOCK{}, nil, windows.FILE_ATTRIBUTE_NORMAL, 0,
		windows.FILE_CREATE, options, 0, 0)
	if err != nil {
		return errno(err)
	}
	defer windows.CloseHandle(handle)
	headerSize := int(unsafe.Sizeof(reparseDataBuffer{}))
	buf := make([]byte, headerSize+2*(len(substitute16)+len(print16)))
	rdb := (*reparseDataBuffer)(unsafe.Pointer(&buf[0]))
	rdb.ReparseTag = windows.IO_REPARSE_TAG_SYMLINK
	rdb.ReparseDataLength = uint16(len(buf) - 8)
	rdb.SubstituteLength = uint16(2 * len(substitute16))
	rdb.PrintOffset = rdb.SubstituteLength
	rdb.PrintLength = uint16(2 * len(print16))
	if len(substitute) == 0 || !isAbsoluteWindowsPath(substitute) {
		rdb.Flags = symlinkFlagRelative
	}
	pathBuffer := unsafe.Slice((*uint16)(unsafe.Pointer(&buf[headerSize])), len(substitute16)+len(print16))
	copy(pathBuffer[:len(substitute16):len(substitute16)], substitute16)
	copy(pathBuffer[len(substitute16):len(substitute16)+len(print16)], print16)
	err = windows.DeviceIoControl(handle, windows.FSCTL_SET_REPARSE_POINT, &buf[0], uint32(len(buf)), nil, 0, nil, nil)
	if err != nil {
		deleteFile := byte(1)
		_ = windows.NtSetInformationFile(handle, &windows.IO_STATUS_BLOCK{}, &deleteFile, 1, fileDispositionInformation)
	}
	return err
}

func isAbsoluteWindowsPath(path string) bool {
	return len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') ||
		len(path) >= 2 && (path[0] == '\\' || path[0] == '/') && (path[1] == '\\' || path[1] == '/')
}

func withSymlinkPrivilege(fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.ImpersonateSelf(windows.SecurityImpersonation); err != nil {
		return fn()
	}
	defer windows.RevertToSelf()
	thread, err := windows.GetCurrentThread()
	if err != nil {
		return fn()
	}
	var token windows.Token
	if err := windows.OpenThreadToken(thread, windows.TOKEN_QUERY|windows.TOKEN_ADJUST_PRIVILEGES, false, &token); err != nil {
		return fn()
	}
	defer token.Close()
	name, err := windows.UTF16PtrFromString("SeCreateSymbolicLinkPrivilege")
	if err != nil {
		return fn()
	}
	var privileges windows.Tokenprivileges
	if err := windows.LookupPrivilegeValue(nil, name, &privileges.Privileges[0].Luid); err != nil {
		return fn()
	}
	privileges.PrivilegeCount = 1
	privileges.Privileges[0].Attributes = windows.SE_PRIVILEGE_ENABLED
	if err := windows.AdjustTokenPrivileges(token, false, &privileges, 0, nil, nil); err != nil {
		return fn()
	}
	return fn()
}

func ReadlinkAt(root windows.Handle, name string, buf []byte) (int, error) {
	h, err := OpenAt(root, name, os.O_RDONLY, 0, false, false, true)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(h)
	raw := make([]byte, windows.MAXIMUM_REPARSE_DATA_BUFFER_SIZE)
	var returned uint32
	if err := windows.DeviceIoControl(h, windows.FSCTL_GET_REPARSE_POINT, nil, 0, &raw[0], uint32(len(raw)), &returned, nil); err != nil {
		return 0, err
	}
	if returned < 20 {
		return 0, syscall.EINVAL
	}
	rdb := (*reparseDataBuffer)(unsafe.Pointer(&raw[0]))
	if rdb.ReparseTag != windows.IO_REPARSE_TAG_SYMLINK {
		return 0, syscall.EINVAL
	}
	start := 20 + int(rdb.PrintOffset)
	end := start + int(rdb.PrintLength)
	if start < 20 || end > int(returned) || end < start {
		return 0, syscall.EINVAL
	}
	text := windows.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(&raw[start])), int(rdb.PrintLength/2)))
	return copy(buf, text), nil
}
