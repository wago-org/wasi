//go:build windows

package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wago-org/wasi/internal/winfs"
	"golang.org/x/sys/windows"
)

const (
	hostOpenReadOnly  = os.O_RDONLY
	hostOpenDirectory = 1 << 24
	hostOpenNoFollow  = 1 << 25
)

func openAt(d *fdEntry, name string, flags int, mode uint32) (*os.File, uint64) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, wasiENotcapable
	}
	parent, leaf := filepath.Split(clean)
	parent = strings.TrimSuffix(parent, string(filepath.Separator))
	if parent != "" {
		p, code := openAt(d, parent, hostOpenReadOnly|hostOpenDirectory, 0)
		if code != wasiOK {
			return nil, code
		}
		defer p.Close()
		return openAt(&fdEntry{file: p, mount: d.mount, root: d.root}, leaf, flags, mode)
	}
	openFlags := flags &^ (hostOpenDirectory | hostOpenNoFollow | os.O_TRUNC)
	root := windows.Handle(d.file.Fd())
	var h windows.Handle
	var err error
	if flags&os.O_CREATE != 0 && flags&os.O_EXCL == 0 {
		h, err = winfs.OpenAt(root, leaf, openFlags&^os.O_CREATE, mode,
			flags&hostOpenDirectory != 0, false, flags&hostOpenNoFollow != 0)
		if errors.Is(err, os.ErrNotExist) {
			h, err = winfs.OpenAt(root, leaf, openFlags, mode,
				flags&hostOpenDirectory != 0, true, flags&hostOpenNoFollow != 0)
		}
	} else {
		h, err = winfs.OpenAt(root, leaf, openFlags, mode,
			flags&hostOpenDirectory != 0, flags&os.O_CREATE != 0, flags&hostOpenNoFollow != 0)
	}
	if err != nil {
		if flags&os.O_CREATE != 0 && flags&os.O_EXCL != 0 && errors.Is(err, syscall.ELOOP) {
			return nil, wasiEExist
		}
		if err == windows.ERROR_INVALID_REPARSE_DATA {
			return nil, wasiENotcapable
		}
		return nil, capabilityErr(err)
	}
	f := os.NewFile(uintptr(h), name)
	finalPath, err := windowsFilePath(f)
	if err != nil || !withinWindowsRoot(d.root, finalPath) {
		_ = f.Close()
		return nil, wasiENotcapable
	}
	if flags&hostOpenNoFollow != 0 {
		info, statErr := f.Stat()
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 {
			_ = f.Close()
			if statErr != nil {
				return nil, errno(statErr)
			}
			return nil, wasiELoop
		}
	}
	if flags&os.O_TRUNC != 0 {
		if err := f.Truncate(0); err != nil {
			_ = f.Close()
			return nil, errno(err)
		}
	}
	if flags&hostOpenDirectory != 0 {
		info, statErr := f.Stat()
		if statErr != nil || !info.IsDir() {
			_ = f.Close()
			if statErr != nil {
				return nil, errno(statErr)
			}
			return nil, wasiENotdir
		}
	}
	return f, wasiOK
}

func openPreopen(path string) (*os.File, error) {
	return openWindowsPath(path, os.O_RDONLY, true, false)
}

func hostMountRoot(file *os.File, _ string) (string, error) {
	return windowsFilePath(file)
}

func openWindowsPath(path string, flags int, directory, reparse bool) (*os.File, error) {
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
	if flags&os.O_APPEND != 0 {
		access |= windows.FILE_APPEND_DATA
		access &^= windows.FILE_WRITE_DATA
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
	if reparse {
		attributes |= windows.FILE_FLAG_OPEN_REPARSE_POINT
	}
	h, err := windows.CreateFile(name, access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, creation, attributes, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

func withinWindowsRoot(root, candidate string) bool {
	root = normalizeWindowsPath(root)
	candidate = normalizeWindowsPath(candidate)
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func normalizeWindowsPath(name string) string {
	if strings.HasPrefix(name, `\\?\UNC\`) {
		return `\\` + name[len(`\\?\UNC\`):]
	}
	return strings.TrimPrefix(name, `\\?\`)
}

func openMetadataAt(d *fdEntry, name string, follow bool) (*os.File, uint64) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, wasiENotcapable
	}
	parent, leaf := filepath.Split(clean)
	parent = strings.TrimSuffix(parent, string(filepath.Separator))
	if parent != "" {
		p, code := openAt(d, parent, hostOpenReadOnly|hostOpenDirectory, 0)
		if code != wasiOK {
			return nil, code
		}
		defer p.Close()
		return openMetadataAt(&fdEntry{file: p, mount: d.mount, root: d.root}, leaf, follow)
	}
	h, err := winfs.OpenAt(windows.Handle(d.file.Fd()), leaf, os.O_RDONLY, 0, false, false, !follow)
	if err != nil {
		return nil, capabilityErr(err)
	}
	f := os.NewFile(uintptr(h), name)
	finalPath, err := windowsFilePath(f)
	if err != nil || !withinWindowsRoot(d.root, finalPath) {
		_ = f.Close()
		return nil, wasiENotcapable
	}
	return f, wasiOK
}

func directoryEntryInfo(_ *fdEntry, entry os.DirEntry) (os.FileInfo, uint64) {
	info, err := entry.Info()
	return info, errno(err)
}

func hostFileStat(info os.FileInfo) (dev, ino, nlink uint64, atim, ctim int64) {
	nlink = 1
	atim, ctim = info.ModTime().UnixNano(), info.ModTime().UnixNano()
	if st, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		atim = st.LastAccessTime.Nanoseconds()
		ctim = st.CreationTime.Nanoseconds()
	}
	return
}

func hostAccessTime(info os.FileInfo) time.Time {
	if st, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return time.Unix(0, st.LastAccessTime.Nanoseconds())
	}
	return info.ModTime()
}

func hostInode(os.FileInfo) uint64 { return 0 }

func setFileTimes(file *os.File, times []time.Time) error {
	atime := windows.NsecToFiletime(times[0].UnixNano())
	mtime := windows.NsecToFiletime(times[1].UnixNano())
	return windows.SetFileTime(windows.Handle(file.Fd()), nil, &atime, &mtime)
}

func setPathTimes(parent *os.File, leaf string, times []time.Time, noFollow bool) error {
	h, err := winfs.OpenAt(windows.Handle(parent.Fd()), leaf, os.O_RDONLY, 0, false, false, noFollow)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(h), leaf)
	defer f.Close()
	return setFileTimes(f, times)
}

func setHostFileFlags(entry *fdEntry, file *os.File, flags uint16) error {
	if flags&4 != 0 {
		return hostErrno.EINVAL
	}
	if flags&1 == entry.flags&1 {
		return nil
	}
	access := uint32(0)
	if entry.rights&rightFDRead != 0 {
		access |= windows.GENERIC_READ
	}
	if entry.rights&rightFDWrite != 0 {
		if flags&1 != 0 {
			access |= windows.FILE_APPEND_DATA | windows.FILE_WRITE_ATTRIBUTES | windows.FILE_WRITE_EA | windows.SYNCHRONIZE
		} else {
			access |= windows.GENERIC_WRITE
		}
	}
	position, _ := file.Seek(0, os.SEEK_CUR)
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("ReOpenFile")
	h, _, callErr := proc.Call(uintptr(file.Fd()), uintptr(access),
		uintptr(windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE),
		uintptr(windows.FILE_FLAG_BACKUP_SEMANTICS))
	if windows.Handle(h) == windows.InvalidHandle {
		return callErr
	}
	reopened := os.NewFile(h, file.Name())
	if position >= 0 {
		_, _ = reopened.Seek(position, os.SEEK_SET)
	}
	entry.file = reopened
	if entry.reader == file {
		entry.reader = reopened
	}
	if entry.writer == file {
		entry.writer = reopened
	}
	return file.Close()
}

func appendWriteAt(file *os.File, b []byte, _ int64) (int, error) {
	return file.Write(b)
}

func makeDirectoryAt(parent *os.File, name string, mode uint32) error {
	return winfs.MkdirAt(windows.Handle(parent.Fd()), name)
}

func linkAt(oldParent *os.File, oldName string, newParent *os.File, newName string) error {
	return winfs.LinkAt(windows.Handle(oldParent.Fd()), oldName, windows.Handle(newParent.Fd()), newName)
}

func linkAtFollow(*fdEntry, string, *os.File, string) uint64 { return wasiENotsup }

func readlinkAt(parent *os.File, name string, buf []byte) (int, error) {
	return winfs.ReadlinkAt(windows.Handle(parent.Fd()), name, buf)
}

func removeAt(parent *os.File, name string, directory bool) error {
	return winfs.DeleteAt(windows.Handle(parent.Fd()), name, directory)
}

func renameAt(oldParent *os.File, oldName string, newParent *os.File, newName string) error {
	return winfs.RenameAt(windows.Handle(oldParent.Fd()), oldName, windows.Handle(newParent.Fd()), newName)
}

func symlinkAt(target string, parent *os.File, name string) error {
	root := windows.Handle(parent.Fd())
	return winfs.SymlinkAt(target, root, name, winfs.TargetIsDirectory(root, target))
}

func windowsFilePath(file *os.File) (string, error) {
	buf := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(windows.Handle(file.Fd()), &buf[0], uint32(len(buf)), 0)
	if err != nil {
		return "", err
	}
	if n == 0 || n >= uint32(len(buf)) {
		return "", hostErrno.ENAMETOOLONG
	}
	return windows.UTF16ToString(buf[:n]), nil
}

func allocateFile(file *os.File, offset, length int64) error {
	end := offset + length
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() >= end {
		return nil
	}
	return file.Truncate(end)
}
