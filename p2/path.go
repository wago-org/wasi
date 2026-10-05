package p2

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func openUnderPathFlags(dir *os.File, name string, flags int, mode, pathFlags uint32) (*os.File, error) {
	if pathFlags&^uint32(1) != 0 {
		return nil, hostFS.EINVAL
	}
	if strings.IndexByte(name, 0) >= 0 || path.IsAbs(name) {
		return nil, hostFS.EPERM
	}
	if name == "" {
		name = "."
	}
	return platformOpenUnderPathFlags(dir, name, flags, mode, pathFlags&1 != 0)
}

// descriptorFileName retains parent steps after links for lazy DirEntry.Info.
func descriptorFileName(dir *os.File, name string) string {
	if name == "." {
		return dir.Name()
	}
	return dir.Name() + string(os.PathSeparator) + filepath.FromSlash(name)
}

// openUnderWalk opens every component without following links in the kernel.
// Link targets are expanded separately and restarted from the pinned base.
// Parent steps are processed after preceding symlinks, and cannot pass the base.
func openUnderWalk(dir *os.File, name string, flags int, mode uint32, follow bool) (*os.File, error) {
	result, err := walkUnder(dir, name, flags, mode, follow, nil)
	return result.file, err
}

type pathWalkResult struct {
	file *os.File
	info os.FileInfo
}

func walkUnder(dir *os.File, name string, flags int, mode uint32, follow bool, statLeaf func(*os.File, string) (os.FileInfo, error)) (pathWalkResult, error) {
	symlinks := 0
resolve:
	for {
		parts := strings.Split(name, "/")
		cur, err := dupFile(dir)
		if err != nil {
			return pathWalkResult{}, err
		}
		for i, part := range parts {
			if part == "" || part == "." {
				continue
			}
			if part == ".." {
				prefix := path.Clean(strings.Join(parts[:i], "/"))
				cur.Close()
				if prefix == "." {
					return pathWalkResult{}, hostFS.EPERM
				}
				name = path.Dir(prefix) + "/" + strings.Join(parts[i+1:], "/")
				continue resolve
			}
			last := i == len(parts)-1
			openFlags, openMode := hostFS.O_RDONLY|hostFS.O_DIRECTORY, uint32(0)
			if last {
				openFlags, openMode = flags, mode
			}
			var fd int
			var openErr error
			if last && statLeaf != nil {
				info, err := statLeaf(cur, part)
				if err != nil {
					cur.Close()
					return pathWalkResult{}, err
				}
				if !follow || info.Mode()&os.ModeSymlink == 0 {
					cur.Close()
					return pathWalkResult{info: info}, nil
				}
				openErr = hostFS.ELOOP
			} else {
				fd, openErr = hostFS.Openat(int(cur.Fd()), part, openFlags|hostFS.O_NOFOLLOW|hostFS.O_CLOEXEC, openMode)
			}
			if openErr == nil {
				fileName := part
				if last {
					fileName = descriptorFileName(dir, name)
				}
				next := os.NewFile(uintptr(fd), fileName)
				// Metadata handles can open a symlink itself. When the caller
				// requests its target, expand it through the same confined walk.
				if last && follow && flags&metadataHandleFlag != 0 {
					info, statErr := next.Stat()
					if statErr != nil {
						next.Close()
						cur.Close()
						return pathWalkResult{}, statErr
					}
					if info.Mode()&os.ModeSymlink != 0 {
						next.Close()
						openErr = hostFS.ELOOP
					}
				}
				if openErr == nil {
					cur.Close()
					if last {
						return pathWalkResult{file: next}, nil
					}
					cur = next
					continue
				}
			}
			if last && (!follow || flags&(hostFS.O_CREAT|hostFS.O_EXCL) == hostFS.O_CREAT|hostFS.O_EXCL) {
				cur.Close()
				return pathWalkResult{}, openErr
			}
			var buf [4096]byte
			n, linkErr := readlinkTargetAt(int(cur.Fd()), part, buf[:])
			cur.Close()
			if linkErr != nil {
				if errors.Is(linkErr, hostFS.EPERM) {
					return pathWalkResult{}, linkErr
				}
				return pathWalkResult{}, openErr
			}
			if n == len(buf) {
				return pathWalkResult{}, hostFS.ENAMETOOLONG
			}
			target, err := normalizeSymlinkPath(string(buf[:n]))
			if err != nil {
				return pathWalkResult{}, err
			}
			if target == "" {
				return pathWalkResult{}, hostFS.ENOENT
			}
			symlinks++
			if symlinks > 40 {
				return pathWalkResult{}, hostFS.ELOOP
			}
			prefix := path.Clean(strings.Join(parts[:i], "/"))
			name = prefix + "/" + target
			if i+1 < len(parts) {
				name += "/" + strings.Join(parts[i+1:], "/")
			}
			continue resolve
		}
		if statLeaf != nil {
			info, err := statLeaf(cur, ".")
			cur.Close()
			return pathWalkResult{info: info}, err
		}
		fd, err := hostFS.Openat(int(cur.Fd()), ".", flags|hostFS.O_NOFOLLOW|hostFS.O_CLOEXEC, mode)
		cur.Close()
		if err != nil {
			return pathWalkResult{}, err
		}
		fileName := dir.Name()
		if path.Clean(name) != "." {
			fileName = descriptorFileName(dir, name)
		}
		return pathWalkResult{file: os.NewFile(uintptr(fd), fileName)}, nil
	}
}
