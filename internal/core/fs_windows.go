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
	guestBackslashSeparator    = true
	hostOpenReadOnly           = os.O_RDONLY
	hostOpenDirectory          = 1 << 24
	hostOpenNoFollow           = 1 << 25
	hostOpenNonblock           = 0
	hostOpenCanCreateDirectory = true
	hostOpenWriteAttributes    = 1 << 26
)

func openAt(d *fdEntry, name string, flags int, mode uint32) (*os.File, uint64) {
	name = filepath.FromSlash(name)
	if filepath.IsAbs(name) || filepath.VolumeName(name) != "" || strings.HasPrefix(name, `\`) {
		return nil, wasiENotcapable
	}
	name = strings.TrimRight(name, `\`)
	if windowsParentStep(name) {
		return walkWindowsParents(d, name, flags, mode, false, flags&hostOpenNoFollow == 0)
	}
	parent, leaf := filepath.Split(name)
	parent = strings.TrimSuffix(parent, string(filepath.Separator))
	if parent != "" {
		p, code := openAt(d, parent, hostOpenReadOnly|hostOpenDirectory, 0)
		if code != wasiOK {
			return nil, code
		}
		defer p.Close()
		return openAt(&fdEntry{file: p, mount: d.mount, root: d.root}, leaf, flags, mode)
	}
	openFlags := flags &^ (hostOpenDirectory | hostOpenNoFollow | hostOpenWriteAttributes | os.O_TRUNC)
	root := windows.Handle(d.file.Fd())
	var h windows.Handle
	var err error
	if flags&os.O_CREATE != 0 && flags&os.O_EXCL == 0 {
		h, err = winfs.OpenAtAccess(root, leaf, openFlags&^os.O_CREATE, mode,
			flags&hostOpenDirectory != 0, false, flags&hostOpenNoFollow != 0, windowsAccess(flags))
		if errors.Is(err, os.ErrNotExist) {
			h, err = winfs.OpenAtAccess(root, leaf, openFlags, mode,
				flags&hostOpenDirectory != 0, true, flags&hostOpenNoFollow != 0, windowsAccess(flags))
		}
	} else {
		h, err = winfs.OpenAtAccess(root, leaf, openFlags, mode,
			flags&hostOpenDirectory != 0, flags&os.O_CREATE != 0, flags&hostOpenNoFollow != 0, windowsAccess(flags))
	}
	if err != nil {
		// Some NT filesystem implementations report a missing name for an
		// existing non-directory when FILE_DIRECTORY_FILE is requested.
		// Distinguish that case through a pinned metadata handle.
		if flags&hostOpenDirectory != 0 && flags&os.O_CREATE == 0 && errors.Is(err, os.ErrNotExist) {
			if existing, code := openMetadataAt(d, leaf, false); code == wasiOK {
				info, statErr := existing.Stat()
				_ = existing.Close()
				if statErr == nil && !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
					return nil, wasiENotdir
				}
			}
		}
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
		if err := setFileSize(f, 0); err != nil {
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

// windowsParentStep identifies paths that need WASI component lookup instead
// of Windows lexical parent normalization. The ordinary leaf path allocates
// no component slice.
func windowsParentStep(name string) bool {
	for start := 0; start < len(name); {
		end := strings.IndexByte(name[start:], '\\')
		if end < 0 {
			end = len(name)
		} else {
			end += start
		}
		if name[start:end] == ".." {
			return true
		}
		start = end + 1
	}
	return false
}

// walkWindowsParents expands relative symlinks before processing parent
// steps. Every directory is opened without following a reparse point first,
// and parent steps return to a pinned handle rather than opening "..".
func walkWindowsParents(d *fdEntry, name string, flags int, mode uint32, metadata, follow bool) (*os.File, uint64) {
	var current *os.File
	var parentInline [8]*os.File
	parents := parentInline[:0]
	defer func() {
		if current != nil && current != d.file {
			_ = current.Close()
		}
		for _, parent := range parents {
			if parent != d.file {
				_ = parent.Close()
			}
		}
	}()
	symlinks := 0
resolve:
	for {
		current = d.file
		parts := strings.Split(name, `\`)
		resolved := make([]string, 0, len(parts))
		for i, part := range parts {
			if part == "" || part == "." {
				continue
			}
			if part == ".." {
				if len(parents) == 0 {
					return nil, wasiENotcapable
				}
				if current != d.file {
					_ = current.Close()
				}
				current = parents[len(parents)-1]
				parents = parents[:len(parents)-1]
				resolved = resolved[:len(resolved)-1]
				continue
			}
			last := i == len(parts)-1
			if !last && len(parents) >= maxPinnedPathDepth {
				return nil, wasiENametoolong
			}
			entry := &fdEntry{file: current, mount: d.mount, root: d.root}
			openFlags, openMode := hostOpenReadOnly|hostOpenDirectory|hostOpenNoFollow, uint32(0)
			if last {
				openFlags, openMode = flags|hostOpenNoFollow, mode
			}
			var next *os.File
			var code uint64
			if last && metadata {
				next, code = openMetadataAt(entry, part, false)
				if code == wasiOK && follow {
					info, err := next.Stat()
					if err != nil {
						_ = next.Close()
						return nil, errno(err)
					}
					if info.Mode()&os.ModeSymlink != 0 {
						_ = next.Close()
						next = nil
						code = wasiELoop
					}
				}
			} else {
				next, code = openAt(entry, part, openFlags, openMode)
			}
			if code == wasiOK {
				if last {
					if current != d.file {
						_ = current.Close()
					}
					current = nil
					return next, wasiOK
				}
				parents = append(parents, current)
				current = next
				resolved = append(resolved, part)
				continue
			}
			if last && (!follow || flags&(os.O_CREATE|os.O_EXCL) == os.O_CREATE|os.O_EXCL) {
				return nil, code
			}
			target, relative, err := winfs.ReadlinkTargetAt(windows.Handle(current.Fd()), part)
			if err != nil {
				return nil, code
			}
			// Absolute targets and junctions cannot be replayed relative to
			// the confined base. Refuse them in this fallback walk.
			if !relative || filepath.IsAbs(target) || filepath.VolumeName(target) != "" || strings.HasPrefix(target, `\`) || strings.HasPrefix(target, "/") {
				return nil, wasiENotcapable
			}
			symlinks++
			if symlinks > 40 {
				return nil, wasiELoop
			}
			expanded := append([]string(nil), resolved...)
			expanded = append(expanded, filepath.FromSlash(target))
			expanded = append(expanded, parts[i+1:]...)
			name = strings.Join(expanded, `\`)
			if current != d.file {
				_ = current.Close()
			}
			for _, parent := range parents {
				if parent != d.file {
					_ = parent.Close()
				}
			}
			parents = parents[:0]
			current = nil
			continue resolve
		}
		entry := &fdEntry{file: current, mount: d.mount, root: d.root}
		if metadata {
			return openMetadataAt(entry, ".", follow)
		}
		return openAt(entry, ".", flags, mode)
	}
}

func windowsAccess(flags int) uint32 {
	if flags&hostOpenWriteAttributes != 0 {
		return windows.FILE_WRITE_ATTRIBUTES
	}
	return 0
}

func openPreopen(path string, writeAttributes bool) (*os.File, error) {
	flags := os.O_RDONLY
	if writeAttributes {
		flags |= hostOpenWriteAttributes
	}
	return openWindowsPath(path, flags, true, false)
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
	access |= windowsAccess(flags)
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
	name = filepath.FromSlash(name)
	if filepath.IsAbs(name) || filepath.VolumeName(name) != "" || strings.HasPrefix(name, `\`) {
		return nil, wasiENotcapable
	}
	name = strings.TrimRight(name, `\`)
	if windowsParentStep(name) {
		return walkWindowsParents(d, name, 0, 0, true, follow)
	}
	parent, leaf := filepath.Split(name)
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
	h, err := winfs.OpenAtAccess(windows.Handle(parent.Fd()), leaf, os.O_RDONLY, 0, false, false, noFollow, windows.FILE_WRITE_ATTRIBUTES)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(h), leaf)
	defer f.Close()
	return setFileTimes(f, times)
}

var reopenFileProc = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReOpenFile")

func reopenWindowsFile(file *os.File, access uint32) (windows.Handle, error) {
	h, _, err := reopenFileProc.Call(uintptr(file.Fd()), uintptr(access),
		uintptr(windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE),
		uintptr(windows.FILE_FLAG_BACKUP_SEMANTICS))
	if windows.Handle(h) == windows.InvalidHandle {
		return windows.InvalidHandle, err
	}
	return windows.Handle(h), nil
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
	if entry.rights&(rightFDWrite|rightFDAllocate|rightFDFilestatSetSize) != 0 {
		if flags&1 != 0 {
			access |= windows.FILE_APPEND_DATA | windows.FILE_WRITE_ATTRIBUTES | windows.FILE_WRITE_EA | windows.SYNCHRONIZE
		} else {
			access |= windows.GENERIC_WRITE
		}
	}
	if entry.rights&rightFDFilestatSetTimes != 0 {
		access |= windows.FILE_WRITE_ATTRIBUTES
	}
	position, _ := file.Seek(0, os.SEEK_CUR)
	h, err := reopenWindowsFile(file, access)
	if err != nil {
		return err
	}
	reopened := os.NewFile(uintptr(h), file.Name())
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
	return setFileSize(file, end)
}

func setFileSize(file *os.File, size int64) error {
	err := file.Truncate(size)
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return err
	}
	// APPEND handles intentionally lack FILE_WRITE_DATA so concurrent writes
	// append atomically. Reopen this same file for the size change without
	// replacing the append handle or disturbing its current position.
	h, err := reopenWindowsFile(file, windows.GENERIC_WRITE)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.Ftruncate(h, size)
}
