//go:build linux

package p2

import (
	"errors"
	"os"
	"path"

	"golang.org/x/sys/unix"
)

func platformOpenUnderPathFlags(dir *os.File, name string, flags int, mode uint32, follow bool) (*os.File, error) {
	if !follow {
		flags |= unix.O_NOFOLLOW
	}
	if flags&unix.O_CREAT == 0 {
		mode = 0
	}
	fd, err := unix.Openat2(int(dir.Fd()), name, &unix.OpenHow{
		Flags: uint64(flags | unix.O_CLOEXEC), Mode: uint64(mode),
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS,
	})
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.E2BIG) || errors.Is(err, unix.EPERM) {
		return openUnderWalk(dir, name, flags, mode, follow)
	}
	if errors.Is(err, unix.EXDEV) {
		return nil, hostFS.EPERM
	}
	if err != nil {
		return nil, err
	}
	clean := path.Clean(name)
	fileName := path.Base(clean)
	if clean == "." {
		fileName = dir.Name()
	}
	return os.NewFile(uintptr(fd), fileName), nil
}
