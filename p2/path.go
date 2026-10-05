package p2

import (
	"errors"
	"os"
	"path"
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

// openUnderWalk opens every component without following links in the kernel.
// Link targets are expanded separately and restarted from the pinned base.
// Parent steps are processed after preceding symlinks, and cannot pass the base.
func openUnderWalk(dir *os.File, name string, flags int, mode uint32, follow bool) (*os.File, error) {
	symlinks := 0
resolve:
	for {
		parts := strings.Split(name, "/")
		cur, err := dupFile(dir)
		if err != nil {
			return nil, err
		}
		for i, part := range parts {
			if part == "" || part == "." {
				continue
			}
			if part == ".." {
				prefix := path.Clean(strings.Join(parts[:i], "/"))
				cur.Close()
				if prefix == "." {
					return nil, hostFS.EPERM
				}
				name = path.Dir(prefix) + "/" + strings.Join(parts[i+1:], "/")
				continue resolve
			}
			last := i == len(parts)-1
			openFlags, openMode := hostFS.O_RDONLY|hostFS.O_DIRECTORY, uint32(0)
			if last {
				openFlags, openMode = flags, mode
			}
			fd, openErr := hostFS.Openat(int(cur.Fd()), part, openFlags|hostFS.O_NOFOLLOW|hostFS.O_CLOEXEC, openMode)
			if openErr == nil {
				next := os.NewFile(uintptr(fd), part)
				cur.Close()
				if last {
					return next, nil
				}
				cur = next
				continue
			}
			if last && (!follow || flags&(hostFS.O_CREAT|hostFS.O_EXCL) == hostFS.O_CREAT|hostFS.O_EXCL) {
				cur.Close()
				return nil, openErr
			}
			var buf [4096]byte
			n, linkErr := readlinkTargetAt(int(cur.Fd()), part, buf[:])
			cur.Close()
			if linkErr != nil {
				if errors.Is(linkErr, hostFS.EPERM) {
					return nil, linkErr
				}
				return nil, openErr
			}
			if n == len(buf) {
				return nil, hostFS.ENAMETOOLONG
			}
			target, err := normalizeSymlinkPath(string(buf[:n]))
			if err != nil {
				return nil, err
			}
			if target == "" {
				return nil, hostFS.ENOENT
			}
			symlinks++
			if symlinks > 40 {
				return nil, hostFS.ELOOP
			}
			prefix := path.Clean(strings.Join(parts[:i], "/"))
			name = prefix + "/" + target
			if i+1 < len(parts) {
				name += "/" + strings.Join(parts[i+1:], "/")
			}
			continue resolve
		}
		fd, err := hostFS.Openat(int(cur.Fd()), ".", flags|hostFS.O_NOFOLLOW|hostFS.O_CLOEXEC, mode)
		cur.Close()
		if err != nil {
			return nil, err
		}
		fileName := name
		if path.Clean(name) == "." {
			fileName = dir.Name()
		}
		return os.NewFile(uintptr(fd), fileName), nil
	}
}
