//go:build windows

package p2

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/wago-org/wasi/internal/winfs"
	"golang.org/x/sys/windows"
)

var (
	errReadOnly       = errors.New("read-only filesystem")
	errInvalid        = errors.New("invalid argument")
	errOverflow       = errors.New("value too large")
	errQuota          = errors.New("descriptor quota exceeded")
	errNotPermitted   = errors.New("operation not permitted")
	errWouldBlock     = errors.New("operation would block")
	errAlready        = errors.New("operation already in progress")
	errBadDescriptor  = errors.New("bad descriptor")
	errBusy           = errors.New("resource busy")
	errDeadlock       = errors.New("deadlock")
	errFileTooLarge   = errors.New("file too large")
	errIllegalByte    = errors.New("illegal byte sequence")
	errInProgress     = errors.New("operation in progress")
	errInterrupted    = errors.New("interrupted")
	errIsDirectory    = errors.New("is a directory")
	errNotDirectory   = errors.New("not a directory")
	errNotEmpty       = errors.New("directory not empty")
	errLoop           = errors.New("too many symbolic links")
	errNameTooLong    = errors.New("name too long")
	errNoDevice       = errors.New("no device")
	errNoLock         = errors.New("no lock")
	errNoMemory       = errors.New("insufficient memory")
	errNoSpace        = errors.New("insufficient space")
	errNotRecoverable = errors.New("state not recoverable")
	errUnsupported    = errors.New("operation unsupported")
	errNoTTY          = errors.New("not a terminal")
	errNoSuchDevice   = errors.New("no such device")
	errCrossDevice    = errors.New("cross-device operation")
	errPipe           = errors.New("broken pipe")
	errInvalidSeek    = errors.New("invalid seek")
	errTextFileBusy   = errors.New("text file busy")
)

const (
	winDirectory = 1 << 24
	winNoFollow  = 1 << 25
)

var hostFS = filesystemPlatform{
	EROFS: errReadOnly, EINVAL: errInvalid, EOVERFLOW: errOverflow, EMFILE: errQuota,
	EPERM: errNotPermitted, ENOENT: os.ErrNotExist, EACCES: os.ErrPermission,
	EAGAIN: errWouldBlock, EALREADY: errAlready, EBADF: errBadDescriptor,
	EBUSY: errBusy, EDEADLK: errDeadlock, EDQUOT: errQuota, EEXIST: os.ErrExist,
	EFBIG: errFileTooLarge, EILSEQ: errIllegalByte, EINPROGRESS: errInProgress,
	EINTR: errInterrupted, EISDIR: errIsDirectory, ENOTDIR: errNotDirectory,
	ENOTEMPTY: errNotEmpty, ELOOP: errLoop, ENAMETOOLONG: errNameTooLong,
	ENODEV: errNoDevice, ENOLCK: errNoLock, ENOMEM: errNoMemory, ENOSPC: errNoSpace,
	ENOTRECOVERABLE: errNotRecoverable, ENOTSUP: errUnsupported, ENOSYS: errUnsupported,
	ENOTTY: errNoTTY, ENXIO: errNoSuchDevice, EXDEV: errCrossDevice, EPIPE: errPipe,
	ESPIPE: errInvalidSeek, ETXTBSY: errTextFileBusy, ENFILE: errQuota,
	O_RDONLY: os.O_RDONLY, O_RDWR: os.O_RDWR, O_WRONLY: os.O_WRONLY,
	O_DIRECTORY: winDirectory, O_CREAT: os.O_CREATE, O_EXCL: os.O_EXCL,
	O_TRUNC: os.O_TRUNC, O_NOFOLLOW: winNoFollow, O_CLOEXEC: 0, AT_REMOVEDIR: 1,
	Dup: duplicateFileHandle, Openat: openFileAt, Mkdirat: mkdirFileAt,
	Unlinkat: unlinkFileAt, Renameat: renameFileAt, Linkat: linkFileAt,
	Readlinkat: readlinkFileAt, Symlinkat: symlinkFileAt,
}

func duplicateFileHandle(fd int) (int, error) {
	process := windows.CurrentProcess()
	var duplicate windows.Handle
	if err := windows.DuplicateHandle(process, windows.Handle(fd), process, &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return 0, err
	}
	return int(duplicate), nil
}

func openFileAt(fd int, name string, flags int, mode uint32) (int, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || filepath.Base(clean) != clean {
		return 0, errNotPermitted
	}
	openFlags := flags &^ (winDirectory | winNoFollow)
	f, err := openWindowsP2At(windows.Handle(fd), clean, openFlags, flags&winDirectory != 0, flags&winNoFollow != 0)
	if err != nil {
		return 0, err
	}
	if flags&winDirectory != 0 {
		info, statErr := f.Stat()
		if statErr != nil || !info.IsDir() {
			_ = f.Close()
			if statErr != nil {
				return 0, statErr
			}
			return 0, errNotDirectory
		}
	}
	duplicate, duplicateErr := duplicateFileHandle(int(f.Fd()))
	closeErr := f.Close()
	if duplicateErr != nil {
		return 0, duplicateErr
	}
	if closeErr != nil {
		_ = windows.CloseHandle(windows.Handle(duplicate))
		return 0, closeErr
	}
	return duplicate, nil
}

func openWindowsP2At(root windows.Handle, name string, flags int, directory, noFollow bool) (*os.File, error) {
	handle, err := winfs.OpenAt(root, name, flags, 0o666, directory, true, noFollow)
	if err != nil {
		if flags&os.O_CREATE != 0 && flags&os.O_EXCL != 0 && errors.Is(err, syscall.ELOOP) {
			return nil, os.ErrExist
		}
		return nil, err
	}
	f := os.NewFile(uintptr(handle), name)
	if noFollow {
		if info, statErr := f.Stat(); statErr != nil {
			_ = f.Close()
			return nil, statErr
		} else if info.Mode()&os.ModeSymlink != 0 {
			_ = f.Close()
			return nil, errLoop
		}
	}
	if flags&os.O_TRUNC != 0 {
		if err := f.Truncate(0); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	return f, nil
}

func openPreopenDirectory(path string) (*os.File, error) {
	return openWindowsP2Path(path, os.O_RDONLY, true)
}

func openWindowsP2Path(path string, flags int, directory bool) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	access := uint32(windows.GENERIC_READ)
	switch flags & (os.O_WRONLY | os.O_RDWR) {
	case os.O_WRONLY:
		access = windows.GENERIC_WRITE
	case os.O_RDWR:
		access = windows.GENERIC_READ | windows.GENERIC_WRITE
	}
	creation := uint32(windows.OPEN_EXISTING)
	switch {
	case flags&os.O_CREATE != 0 && flags&os.O_EXCL != 0:
		creation = windows.CREATE_NEW
	case flags&os.O_CREATE != 0 && flags&os.O_TRUNC != 0:
		creation = windows.CREATE_ALWAYS
	case flags&os.O_CREATE != 0:
		creation = windows.OPEN_ALWAYS
	case flags&os.O_TRUNC != 0:
		creation = windows.TRUNCATE_EXISTING
	}
	attributes := uint32(windows.FILE_ATTRIBUTE_NORMAL)
	if directory {
		attributes |= windows.FILE_FLAG_BACKUP_SEMANTICS
	}
	h, err := windows.CreateFile(name, access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, creation, attributes, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

func mkdirFileAt(fd int, name string, mode uint32) error {
	return winfs.MkdirAt(windows.Handle(fd), name)
}

func unlinkFileAt(fd int, name string, flags int) error {
	return winfs.DeleteAt(windows.Handle(fd), name, flags != 0)
}

func renameFileAt(oldFD int, oldName string, newFD int, newName string) error {
	return winfs.RenameAt(windows.Handle(oldFD), oldName, windows.Handle(newFD), newName)
}

func linkFileAt(oldFD int, oldName string, newFD int, newName string, _ int) error {
	return winfs.LinkAt(windows.Handle(oldFD), oldName, windows.Handle(newFD), newName)
}

func readlinkFileAt(fd int, name string, buf []byte) (int, error) {
	return winfs.ReadlinkAt(windows.Handle(fd), name, buf)
}

func symlinkFileAt(oldName string, newFD int, newName string) error {
	root := windows.Handle(newFD)
	return winfs.SymlinkAt(oldName, root, newName, winfs.TargetIsDirectory(root, oldName))
}

func platformFilesystemError(err error) (uint32, bool) {
	switch {
	case errors.Is(err, syscall.ELOOP):
		return fsErrLoop, true
	case errors.Is(err, syscall.ENOTDIR):
		return fsErrNotDirectory, true
	case errors.Is(err, syscall.EISDIR):
		return fsErrIsDirectory, true
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return fsErrAccess, true
	case errors.Is(err, windows.ERROR_FILE_NOT_FOUND), errors.Is(err, windows.ERROR_PATH_NOT_FOUND):
		return fsErrNoEntry, true
	case errors.Is(err, windows.ERROR_ALREADY_EXISTS), errors.Is(err, windows.ERROR_FILE_EXISTS):
		return fsErrExist, true
	case errors.Is(err, windows.ERROR_TOO_MANY_OPEN_FILES):
		return fsErrQuota, true
	case errors.Is(err, windows.ERROR_DISK_FULL), errors.Is(err, windows.ERROR_HANDLE_DISK_FULL):
		return fsErrInsufficientSpace, true
	case errors.Is(err, windows.ERROR_DIR_NOT_EMPTY):
		return fsErrNotEmpty, true
	case errors.Is(err, windows.ERROR_DIRECTORY):
		return fsErrNotDirectory, true
	case errors.Is(err, windows.ERROR_INVALID_HANDLE):
		return fsErrBadDescriptor, true
	case errors.Is(err, windows.ERROR_INVALID_PARAMETER):
		return fsErrInvalid, true
	case errors.Is(err, windows.ERROR_NOT_SAME_DEVICE):
		return fsErrCrossDevice, true
	case errors.Is(err, windows.ERROR_BROKEN_PIPE), errors.Is(err, windows.ERROR_NO_DATA):
		return fsErrPipe, true
	case errors.Is(err, windows.ERROR_FILENAME_EXCED_RANGE):
		return fsErrNameTooLong, true
	case errors.Is(err, windows.ERROR_SHARING_VIOLATION), errors.Is(err, windows.ERROR_LOCK_VIOLATION), errors.Is(err, windows.ERROR_BUSY):
		return fsErrBusy, true
	case errors.Is(err, windows.ERROR_NEGATIVE_SEEK):
		return fsErrInvalidSeek, true
	case errors.Is(err, windows.ERROR_NOT_ENOUGH_MEMORY), errors.Is(err, windows.ERROR_OUTOFMEMORY):
		return fsErrInsufficientMemory, true
	default:
		return 0, false
	}
}
