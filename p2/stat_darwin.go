//go:build darwin

package p2

import (
	"io/fs"
	"os"
	"path"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	sysunix "golang.org/x/sys/unix"
)

// Darwin metadata uses fstatat rather than a metadata file handle.
const metadataHandleFlag = 0

func platformStatUnderPathFlags(dir *os.File, name string, follow bool) (os.FileInfo, error) {
	// O_EVTONLY can still require data-read permission and rejects sockets.
	// Resolve intermediate components through the confined walker, then stat
	// the leaf without opening its data stream or following it in the kernel.
	result, err := walkUnder(dir, name, 0, 0, follow, statMetadataLeaf)
	return result.info, err
}

type darwinMetadataInfo struct {
	name string
	stat syscall.Stat_t
}

func statMetadataLeaf(dir *os.File, name string) (os.FileInfo, error) {
	var st sysunix.Stat_t
	if err := sysunix.Fstatat(int(dir.Fd()), name, &st, sysunix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, err
	}
	fileName := name
	if name == "." {
		fileName = dir.Name()
	}
	return &darwinMetadataInfo{name: path.Base(fileName), stat: syscall.Stat_t{
		Dev: st.Dev, Mode: st.Mode, Nlink: st.Nlink, Ino: st.Ino,
		Uid: st.Uid, Gid: st.Gid, Rdev: st.Rdev,
		Atimespec: syscall.Timespec(st.Atim), Mtimespec: syscall.Timespec(st.Mtim),
		Ctimespec: syscall.Timespec(st.Ctim), Birthtimespec: syscall.Timespec(st.Btim),
		Size: st.Size, Blocks: st.Blocks, Blksize: st.Blksize,
		Flags: st.Flags, Gen: st.Gen, Lspare: st.Lspare, Qspare: st.Qspare,
	}}, nil
}

func (i *darwinMetadataInfo) Name() string       { return i.name }
func (i *darwinMetadataInfo) Size() int64        { return i.stat.Size }
func (i *darwinMetadataInfo) ModTime() time.Time { return time.Unix(i.stat.Mtimespec.Unix()) }
func (i *darwinMetadataInfo) IsDir() bool        { return i.Mode().IsDir() }
func (i *darwinMetadataInfo) Sys() any           { return &i.stat }

func (i *darwinMetadataInfo) Mode() fs.FileMode {
	mode := fs.FileMode(i.stat.Mode & 0o777)
	switch i.stat.Mode & sysunix.S_IFMT {
	case sysunix.S_IFDIR:
		mode |= fs.ModeDir
	case sysunix.S_IFLNK:
		mode |= fs.ModeSymlink
	case sysunix.S_IFIFO:
		mode |= fs.ModeNamedPipe
	case sysunix.S_IFSOCK:
		mode |= fs.ModeSocket
	case sysunix.S_IFBLK:
		mode |= fs.ModeDevice
	case sysunix.S_IFCHR:
		mode |= fs.ModeDevice | fs.ModeCharDevice
	case sysunix.S_IFREG:
	default:
		mode |= fs.ModeIrregular
	}
	if i.stat.Mode&sysunix.S_ISUID != 0 {
		mode |= fs.ModeSetuid
	}
	if i.stat.Mode&sysunix.S_ISGID != 0 {
		mode |= fs.ModeSetgid
	}
	if i.stat.Mode&sysunix.S_ISVTX != 0 {
		mode |= fs.ModeSticky
	}
	return mode
}

func hostStat(info fs.FileInfo) (nlink uint64, atime, mtime, ctime time.Time, dev, ino uint64) {
	mtime = info.ModTime()
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink), time.Unix(st.Atimespec.Sec, st.Atimespec.Nsec), mtime,
			time.Unix(st.Ctimespec.Sec, st.Ctimespec.Nsec), uint64(st.Dev), st.Ino
	}
	return 1, mtime, mtime, mtime, 0, 0
}

func setFileTimes(f *os.File, atime, mtime time.Time) error {
	at, err := sysunix.TimeToTimespec(atime)
	if err != nil {
		return hostFS.EOVERFLOW
	}
	mt, err := sysunix.TimeToTimespec(mtime)
	if err != nil {
		return hostFS.EOVERFLOW
	}
	// Darwin's futimens uses fsetattrlist. Attribute order follows the bitmap:
	// modification time precedes access time, each a native 64-bit timespec.
	attrs := sysunix.Attrlist{Bitmapcount: sysunix.ATTR_BIT_MAP_COUNT,
		Commonattr: sysunix.ATTR_CMN_MODTIME | sysunix.ATTR_CMN_ACCTIME}
	times := [2]sysunix.Timespec{mt, at}
	_, _, errno := syscall.Syscall6(sysunix.SYS_FSETATTRLIST, f.Fd(),
		uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&times[0])),
		unsafe.Sizeof(times), 0, 0)
	runtime.KeepAlive(f)
	if errno != 0 {
		return errno
	}
	return nil
}
func syncFileData(f *os.File) error { return sysunix.Fsync(int(f.Fd())) }
