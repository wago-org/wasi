//go:build linux

package core

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func hostMountRoot(_ *os.File, host string) (string, error) { return host, nil }

const secureResolve = unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS

const (
	guestBackslashSeparator    = false
	hostOpenReadOnly           = unix.O_RDONLY
	hostOpenDirectory          = unix.O_DIRECTORY
	hostOpenNoFollow           = unix.O_NOFOLLOW
	hostOpenNonblock           = unix.O_NONBLOCK
	hostOpenWriteAttributes    = 0
	hostOpenCanCreateDirectory = false
)

func openPreopen(path string, _ bool) (*os.File, error) { return os.Open(path) }

func openAt(d *fdEntry, name string, flags int, mode uint32) (*os.File, uint64) {
	if flags&unix.O_CREAT == 0 {
		mode = 0
	}
	fd, err := unix.Openat2(int(d.file.Fd()), name, &unix.OpenHow{
		Flags: uint64(flags | unix.O_CLOEXEC), Mode: uint64(mode), Resolve: secureResolve,
	})
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.E2BIG) || errors.Is(err, unix.EPERM) {
		return openAtWalk(d, name, flags, mode)
	}
	if err != nil {
		return nil, capabilityErr(err)
	}
	return os.NewFile(uintptr(fd), name), wasiOK
}

// openAtWalk is the secure fallback for kernels, seccomp profiles, and syscall
// emulators without openat2. It resolves a final relative symlink from its
// pinned parent, and refuses intermediate symlinks or link targets that could
// climb above that parent. Parent steps pop pinned descriptors, avoiding
// pathname replay and native ".." traversal.
func openAtWalk(d *fdEntry, name string, flags int, mode uint32) (*os.File, uint64) {
	return openAtWalkFD(int(d.file.Fd()), name, flags, mode, 0)
}

func openAtWalkFD(rootFD int, name string, flags int, mode uint32, symlinks int) (*os.File, uint64) {
	if symlinks > 40 {
		return nil, wasiELoop
	}
	if name == "" || strings.HasPrefix(name, "/") || pathEscapes(name) {
		return nil, wasiENotcapable
	}
	curFD, err := unix.Dup(rootFD)
	if err != nil {
		return nil, errno(err)
	}
	var parentInline [8]int
	parents := parentInline[:0]
	defer func() {
		_ = unix.Close(curFD)
		for _, fd := range parents {
			_ = unix.Close(fd)
		}
	}()
	retainParents := false
	if strings.Contains(name, "..") {
		for start := 0; start < len(name); {
			end := strings.IndexByte(name[start:], '/')
			if end < 0 {
				end = len(name)
			} else {
				end += start
			}
			if name[start:end] == ".." {
				retainParents = true
				break
			}
			start = end + 1
		}
	}
	for start := 0; start < len(name); {
		end := strings.IndexByte(name[start:], '/')
		if end < 0 {
			end = len(name)
		} else {
			end += start
		}
		part := name[start:end]
		switch part {
		case "", ".":
		case "..":
			if len(parents) == 0 {
				return nil, wasiENotcapable
			}
			_ = unix.Close(curFD)
			curFD = parents[len(parents)-1]
			parents = parents[:len(parents)-1]
		default:
			if end == len(name) {
				if flags&unix.O_NOFOLLOW == 0 && flags&(unix.O_CREAT|unix.O_EXCL) != unix.O_CREAT|unix.O_EXCL {
					var targetBuf [4096]byte
					n, linkErr := unix.Readlinkat(curFD, part, targetBuf[:])
					if linkErr == nil {
						if n == len(targetBuf) {
							return nil, wasiENametoolong
						}
						return openAtWalkFD(curFD, string(targetBuf[:n]), flags, mode, symlinks+1)
					}
					if !errors.Is(linkErr, unix.EINVAL) && !errors.Is(linkErr, unix.ENOENT) {
						return nil, capabilityErr(linkErr)
					}
				}
				fd, err := unix.Openat(curFD, part, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC, mode)
				if err != nil {
					return nil, capabilityErr(err)
				}
				return os.NewFile(uintptr(fd), name), wasiOK
			}
			if retainParents && len(parents) >= maxPinnedPathDepth {
				return nil, wasiENametoolong
			}
			next, err := unix.Openat(curFD, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				return nil, capabilityErr(err)
			}
			if retainParents {
				parents = append(parents, curFD)
			} else {
				_ = unix.Close(curFD)
			}
			curFD = next
		}
		start = end + 1
	}
	fd, err := unix.Openat(curFD, ".", flags|unix.O_NOFOLLOW|unix.O_CLOEXEC, mode)
	if err != nil {
		return nil, capabilityErr(err)
	}
	return os.NewFile(uintptr(fd), name), wasiOK
}

func openMetadataAt(d *fdEntry, name string, follow bool) (*os.File, uint64) {
	flags := unix.O_PATH
	if !follow {
		flags |= unix.O_NOFOLLOW
	}
	return openAt(d, name, flags, 0)
}

func directoryEntryInfo(parent *fdEntry, entry os.DirEntry) (os.FileInfo, uint64) {
	f, code := openMetadataAt(parent, entry.Name(), false)
	if code != wasiOK {
		return nil, code
	}
	defer f.Close()
	info, err := f.Stat()
	return info, errno(err)
}

func hostFileStat(info os.FileInfo) (dev, ino, nlink uint64, atim, ctim int64) {
	dev, ino, nlink = 1, 1, 1
	atim, ctim = info.ModTime().UnixNano(), info.ModTime().UnixNano()
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		dev, ino, nlink = uint64(st.Dev), st.Ino, uint64(st.Nlink)
		atim = st.Atim.Sec*1e9 + st.Atim.Nsec
		ctim = st.Ctim.Sec*1e9 + st.Ctim.Nsec
	}
	return
}

func hostAccessTime(info os.FileInfo) time.Time {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Atim.Sec, st.Atim.Nsec)
	}
	return info.ModTime()
}

func setFileTimes(file *os.File, times []time.Time) error {
	values := []unix.Timespec{unix.NsecToTimespec(times[0].UnixNano()), unix.NsecToTimespec(times[1].UnixNano())}
	return unix.UtimesNanoAt(int(file.Fd()), "", values, unix.AT_EMPTY_PATH)
}

func setPathTimes(parent *os.File, leaf string, times []time.Time, noFollow bool) error {
	flags := 0
	if noFollow {
		flags = unix.AT_SYMLINK_NOFOLLOW
	}
	values := []unix.Timespec{unix.NsecToTimespec(times[0].UnixNano()), unix.NsecToTimespec(times[1].UnixNano())}
	return unix.UtimesNanoAt(int(parent.Fd()), leaf, values, flags)
}

func hostInode(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}

func setHostFileFlags(_ *fdEntry, file *os.File, flags uint16) error {
	current, err := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	if err != nil {
		return err
	}
	current &^= unix.O_APPEND | unix.O_NONBLOCK
	if flags&1 != 0 {
		current |= unix.O_APPEND
	}
	if flags&4 != 0 {
		current |= unix.O_NONBLOCK
	}
	_, err = unix.FcntlInt(file.Fd(), unix.F_SETFL, current)
	return err
}

func appendWriteAt(file *os.File, b []byte, offset int64) (int, error) {
	return unix.Pwrite(int(file.Fd()), b, offset)
}

func makeDirectoryAt(parent *os.File, name string, mode uint32) error {
	return unix.Mkdirat(int(parent.Fd()), name, mode)
}

func linkAt(oldParent *os.File, oldName string, newParent *os.File, newName string) error {
	return unix.Linkat(int(oldParent.Fd()), oldName, int(newParent.Fd()), newName, 0)
}

func readlinkAt(parent *os.File, name string, buf []byte) (int, error) {
	return unix.Readlinkat(int(parent.Fd()), name, buf)
}

func removeAt(parent *os.File, name string, directory bool) error {
	flags := 0
	if directory {
		flags = unix.AT_REMOVEDIR
	}
	return unix.Unlinkat(int(parent.Fd()), name, flags)
}

func renameAt(oldParent *os.File, oldName string, newParent *os.File, newName string) uint64 {
	err := unix.Renameat(int(oldParent.Fd()), oldName, int(newParent.Fd()), newName)
	// renameat(2) permits EEXIST for a non-empty target directory, but WASI expects ENOTEMPTY.
	if err == unix.EEXIST {
		return wasiENotempty
	}
	return errno(err)
}

func symlinkAt(target string, parent *os.File, name string) error {
	return unix.Symlinkat(target, int(parent.Fd()), name)
}

func linkAtFollow(oldDirectory *fdEntry, oldName string, newParent *os.File, newLeaf string) uint64 {
	// Preview 1 permits implementations to reject link-time symlink following.
	// Refusing it avoids Linux AT_EMPTY_PATH's CAP_DAC_READ_SEARCH requirement
	// and the unsafe /proc/self/fd fallback on hosts without procfs.
	return wasiEInval
}

func allocateFile(file *os.File, offset, length int64) error {
	return unix.Fallocate(int(file.Fd()), 0, offset, length)
}

func setFileSize(file *os.File, size int64) error { return file.Truncate(size) }
