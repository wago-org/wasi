package p2

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	experimentalsys "github.com/tetratelabs/wazero/experimental/sys"
	"github.com/tetratelabs/wazero/experimental/sysfs"
	wazerosys "github.com/tetratelabs/wazero/sys"
	sys "github.com/wago-org/wasi/internal/p2sys"
)

// FSConfig describes the filesystem capabilities granted to a component.
// It is immutable: each With method returns a new configuration.
//
// Guest paths are lexical POSIX paths. Absolute paths and paths containing
// parent traversal never reach the host filesystem. Directory mounts also
// reject symlinks while dereferencing paths, including symlinked intermediate
// directories, so a link cannot redirect a guest operation outside the mount.
type FSConfig interface {
	WithDirMount(hostDir, guestPath string) FSConfig
	WithReadOnlyDirMount(hostDir, guestPath string) FSConfig
	WithFSMount(hostFS fs.FS, guestPath string) FSConfig
	preopens() ([]sys.FS, []string)
}

type fsConfig struct {
	fss        []sys.FS
	guestPaths []string
	indexes    map[string]int
}

// NewFSConfig returns an empty filesystem capability set.
func NewFSConfig() FSConfig { return &fsConfig{indexes: map[string]int{}} }

func (c *fsConfig) WithDirMount(hostDir, guestPath string) FSConfig {
	root := canonicalHostRoot(hostDir)
	return c.withMount(&wazeroFS{fs: sysfs.DirFS(root), hostRoot: root}, guestPath)
}

func (c *fsConfig) WithReadOnlyDirMount(hostDir, guestPath string) FSConfig {
	root := canonicalHostRoot(hostDir)
	return c.withMount(&wazeroFS{fs: &sysfs.ReadFS{FS: sysfs.DirFS(root)}, hostRoot: root}, guestPath)
}

func canonicalHostRoot(hostDir string) string {
	root, err := filepath.Abs(hostDir)
	if err != nil {
		return hostDir
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		return resolved
	}
	return root
}

func (c *fsConfig) WithFSMount(hostFS fs.FS, guestPath string) FSConfig {
	if hostFS == nil {
		return c
	}
	return c.withMount(&wazeroFS{fs: &sysfs.AdaptFS{FS: hostFS}}, guestPath)
}

func (c *fsConfig) withMount(hostFS sys.FS, guestPath string) FSConfig {
	cleaned := stripPrefixesAndTrailingSlash(guestPath)
	clone := &fsConfig{
		fss:        append([]sys.FS(nil), c.fss...),
		guestPaths: append([]string(nil), c.guestPaths...),
		indexes:    make(map[string]int, len(c.indexes)+1),
	}
	for key, value := range c.indexes {
		clone.indexes[key] = value
	}
	if i, ok := clone.indexes[cleaned]; ok {
		clone.fss[i], clone.guestPaths[i] = hostFS, guestPath
	} else {
		clone.indexes[cleaned] = len(clone.fss)
		clone.fss = append(clone.fss, hostFS)
		clone.guestPaths = append(clone.guestPaths, guestPath)
	}
	return clone
}

func (c *fsConfig) preopens() ([]sys.FS, []string) {
	return append([]sys.FS(nil), c.fss...), append([]string(nil), c.guestPaths...)
}

type wazeroFS struct {
	fs       experimentalsys.FS
	hostRoot string
}

// checkHostPath rejects symlinked path components for directory mounts. This
// closes the common escape where root/link points outside root. A final
// symlink is allowed only for operations which act on the link itself.
func (w *wazeroFS) checkHostPath(name string, allowFinalSymlink, allowMissingFinal bool) sys.Errno {
	if w.hostRoot == "" || name == "." {
		return 0
	}
	parts := strings.Split(name, "/")
	current := w.hostRoot
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		final := i == len(parts)-1
		if err != nil {
			if final && allowMissingFinal && os.IsNotExist(err) {
				return 0
			}
			return sys.UnwrapOSError(err)
		}
		if info.Mode()&fs.ModeSymlink != 0 && !(final && allowFinalSymlink) {
			return sys.EPERM
		}
	}
	return 0
}

func validFSPath(name string) bool {
	return name == "." || (name != "" && !strings.HasPrefix(name, "/") && fs.ValidPath(name))
}

func invalidPathErrno(name string) sys.Errno {
	if validFSPath(name) {
		return 0
	}
	return sys.EPERM
}

func (w *wazeroFS) OpenFile(name string, flag sys.Oflag, perm fs.FileMode) (sys.File, sys.Errno) {
	if errno := invalidPathErrno(name); errno != 0 {
		return nil, errno
	}
	if errno := w.checkHostPath(name, false, flag&sys.O_CREAT != 0); errno != 0 {
		return nil, errno
	}
	f, errno := w.fs.OpenFile(name, experimentalsys.Oflag(flag), perm)
	if errno != 0 {
		return nil, sys.Errno(errno)
	}
	return &wazeroFile{file: f}, 0
}

func convertStat(st wazerosys.Stat_t) sys.Stat_t {
	return sys.Stat_t{Dev: st.Dev, Ino: st.Ino, Mode: st.Mode, Nlink: st.Nlink, Size: st.Size, Atim: st.Atim, Mtim: st.Mtim, Ctim: st.Ctim}
}

func (w *wazeroFS) Lstat(name string) (sys.Stat_t, sys.Errno) {
	if errno := invalidPathErrno(name); errno != 0 {
		return sys.Stat_t{}, errno
	}
	if errno := w.checkHostPath(name, true, false); errno != 0 {
		return sys.Stat_t{}, errno
	}
	st, errno := w.fs.Lstat(name)
	return convertStat(st), sys.Errno(errno)
}
func (w *wazeroFS) Stat(name string) (sys.Stat_t, sys.Errno) {
	if errno := invalidPathErrno(name); errno != 0 {
		return sys.Stat_t{}, errno
	}
	if errno := w.checkHostPath(name, false, false); errno != 0 {
		return sys.Stat_t{}, errno
	}
	st, errno := w.fs.Stat(name)
	return convertStat(st), sys.Errno(errno)
}
func (w *wazeroFS) Mkdir(name string, perm fs.FileMode) sys.Errno {
	if errno := invalidPathErrno(name); errno != 0 {
		return errno
	}
	if errno := w.checkHostPath(name, false, true); errno != 0 {
		return errno
	}
	return sys.Errno(w.fs.Mkdir(name, perm))
}
func (w *wazeroFS) Chmod(name string, perm fs.FileMode) sys.Errno {
	if errno := invalidPathErrno(name); errno != 0 {
		return errno
	}
	if errno := w.checkHostPath(name, false, false); errno != 0 {
		return errno
	}
	return sys.Errno(w.fs.Chmod(name, perm))
}
func (w *wazeroFS) Rename(from, to string) sys.Errno {
	if errno := invalidPathErrno(from); errno != 0 {
		return errno
	}
	if errno := invalidPathErrno(to); errno != 0 {
		return errno
	}
	if errno := w.checkHostPath(from, true, false); errno != 0 {
		return errno
	}
	if errno := w.checkHostPath(to, true, true); errno != 0 {
		return errno
	}
	return sys.Errno(w.fs.Rename(from, to))
}
func (w *wazeroFS) Rmdir(name string) sys.Errno {
	if errno := invalidPathErrno(name); errno != 0 {
		return errno
	}
	if errno := w.checkHostPath(name, false, false); errno != 0 {
		return errno
	}
	return sys.Errno(w.fs.Rmdir(name))
}
func (w *wazeroFS) Unlink(name string) sys.Errno {
	if errno := invalidPathErrno(name); errno != 0 {
		return errno
	}
	if errno := w.checkHostPath(name, true, false); errno != 0 {
		return errno
	}
	return sys.Errno(w.fs.Unlink(name))
}
func (w *wazeroFS) Link(oldName, newName string) sys.Errno {
	if errno := invalidPathErrno(oldName); errno != 0 {
		return errno
	}
	if errno := invalidPathErrno(newName); errno != 0 {
		return errno
	}
	if errno := w.checkHostPath(oldName, true, false); errno != 0 {
		return errno
	}
	if errno := w.checkHostPath(newName, true, true); errno != 0 {
		return errno
	}
	return sys.Errno(w.fs.Link(oldName, newName))
}
func (w *wazeroFS) Symlink(oldName, linkName string) sys.Errno {
	if path.IsAbs(oldName) {
		return sys.EPERM
	}
	if errno := invalidPathErrno(linkName); errno != 0 {
		return errno
	}
	if errno := w.checkHostPath(linkName, true, true); errno != 0 {
		return errno
	}
	return sys.Errno(w.fs.Symlink(oldName, linkName))
}
func (w *wazeroFS) Readlink(name string) (string, sys.Errno) {
	if errno := invalidPathErrno(name); errno != 0 {
		return "", errno
	}
	if errno := w.checkHostPath(name, true, false); errno != 0 {
		return "", errno
	}
	target, errno := w.fs.Readlink(name)
	return target, sys.Errno(errno)
}
func (w *wazeroFS) Utimens(name string, atim, mtim int64) sys.Errno {
	if errno := invalidPathErrno(name); errno != 0 {
		return errno
	}
	if errno := w.checkHostPath(name, false, false); errno != 0 {
		return errno
	}
	return sys.Errno(w.fs.Utimens(name, atim, mtim))
}

type wazeroFile struct{ file experimentalsys.File }

func (f *wazeroFile) Dev() (uint64, sys.Errno)    { v, e := f.file.Dev(); return v, sys.Errno(e) }
func (f *wazeroFile) Ino() (sys.Inode, sys.Errno) { v, e := f.file.Ino(); return v, sys.Errno(e) }
func (f *wazeroFile) IsDir() (bool, sys.Errno)    { v, e := f.file.IsDir(); return v, sys.Errno(e) }
func (f *wazeroFile) IsAppend() bool              { return f.file.IsAppend() }
func (f *wazeroFile) SetAppend(v bool) sys.Errno  { return sys.Errno(f.file.SetAppend(v)) }
func (f *wazeroFile) Stat() (sys.Stat_t, sys.Errno) {
	st, e := f.file.Stat()
	return convertStat(st), sys.Errno(e)
}
func (f *wazeroFile) Read(p []byte) (int, sys.Errno) { n, e := f.file.Read(p); return n, sys.Errno(e) }
func (f *wazeroFile) Pread(p []byte, off int64) (int, sys.Errno) {
	n, e := f.file.Pread(p, off)
	return n, sys.Errno(e)
}
func (f *wazeroFile) SeekFile(off int64, whence int) (int64, sys.Errno) {
	n, e := f.file.Seek(off, whence)
	return n, sys.Errno(e)
}
func (f *wazeroFile) Readdir(n int) ([]sys.Dirent, sys.Errno) {
	entries, errno := f.file.Readdir(n)
	out := make([]sys.Dirent, len(entries))
	for i, entry := range entries {
		out[i] = sys.Dirent{Ino: entry.Ino, Name: entry.Name, Type: entry.Type}
	}
	return out, sys.Errno(errno)
}
func (f *wazeroFile) Write(p []byte) (int, sys.Errno) {
	n, e := f.file.Write(p)
	return n, sys.Errno(e)
}
func (f *wazeroFile) Pwrite(p []byte, off int64) (int, sys.Errno) {
	n, e := f.file.Pwrite(p, off)
	return n, sys.Errno(e)
}
func (f *wazeroFile) Truncate(size int64) sys.Errno { return sys.Errno(f.file.Truncate(size)) }
func (f *wazeroFile) Sync() sys.Errno               { return sys.Errno(f.file.Sync()) }
func (f *wazeroFile) Datasync() sys.Errno           { return sys.Errno(f.file.Datasync()) }
func (f *wazeroFile) Utimens(atim, mtim int64) sys.Errno {
	return sys.Errno(f.file.Utimens(atim, mtim))
}
func (f *wazeroFile) Close() sys.Errno { return sys.Errno(f.file.Close()) }
