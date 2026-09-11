//go:build windows

package core

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

var hostErrno = platformErrnos{
	EPERM: syscall.EPERM, E2BIG: syscall.E2BIG, EACCES: syscall.EACCES,
	EAGAIN: syscall.EAGAIN, EINTR: syscall.EINTR, ENOSPC: syscall.ENOSPC,
	EDQUOT: syscall.EDQUOT, ENOMEM: syscall.ENOMEM, EMFILE: syscall.EMFILE,
	ENFILE: syscall.ENFILE, EFBIG: syscall.EFBIG, EOVERFLOW: syscall.EOVERFLOW,
	EBUSY: syscall.EBUSY, EPIPE: syscall.EPIPE, EXDEV: syscall.EXDEV,
	ENOENT: syscall.ENOENT, ENOTEMPTY: syscall.ENOTEMPTY, EEXIST: syscall.EEXIST,
	EBADF: syscall.EBADF, EINVAL: syscall.EINVAL, EISDIR: syscall.EISDIR,
	ENOTDIR: syscall.ENOTDIR, ELOOP: syscall.ELOOP, ENAMETOOLONG: syscall.ENAMETOOLONG,
	EROFS: syscall.EROFS, ESPIPE: syscall.ESPIPE,
}

func platformErrno(err error) (uint64, bool) {
	switch {
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return wasiEAcces, true
	case errors.Is(err, windows.ERROR_FILE_NOT_FOUND), errors.Is(err, windows.ERROR_PATH_NOT_FOUND):
		return wasiENoent, true
	case errors.Is(err, windows.ERROR_ALREADY_EXISTS), errors.Is(err, windows.ERROR_FILE_EXISTS):
		return wasiEExist, true
	case errors.Is(err, windows.ERROR_TOO_MANY_OPEN_FILES):
		return wasiEMfile, true
	case errors.Is(err, windows.ERROR_DISK_FULL), errors.Is(err, windows.ERROR_HANDLE_DISK_FULL):
		return wasiENospc, true
	case errors.Is(err, windows.ERROR_DIR_NOT_EMPTY):
		return wasiENotempty, true
	case errors.Is(err, windows.ERROR_DIRECTORY):
		return wasiENotdir, true
	case errors.Is(err, windows.ERROR_INVALID_HANDLE):
		return wasiEBadf, true
	case errors.Is(err, windows.ERROR_INVALID_PARAMETER):
		return wasiEInval, true
	case errors.Is(err, windows.ERROR_NOT_SAME_DEVICE):
		return wasiEXdev, true
	case errors.Is(err, windows.ERROR_BROKEN_PIPE), errors.Is(err, windows.ERROR_NO_DATA):
		return wasiEPipe, true
	case errors.Is(err, windows.ERROR_FILENAME_EXCED_RANGE):
		return wasiENametoolong, true
	case errors.Is(err, windows.ERROR_SHARING_VIOLATION), errors.Is(err, windows.ERROR_LOCK_VIOLATION), errors.Is(err, windows.ERROR_BUSY):
		return wasiEBusy, true
	case errors.Is(err, windows.ERROR_NEGATIVE_SEEK):
		return wasiESpipe, true
	case errors.Is(err, windows.ERROR_NOT_ENOUGH_MEMORY), errors.Is(err, windows.ERROR_OUTOFMEMORY):
		return wasiENomem, true
	default:
		return 0, false
	}
}
