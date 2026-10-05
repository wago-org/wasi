//go:build linux || windows

package p2

import "os"

func platformStatUnderPathFlags(dir *os.File, name string, follow bool) (os.FileInfo, error) {
	f, err := platformOpenUnderPathFlags(dir, name, metadataOpenFlags, 0, follow)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}

func statMetadataLeaf(dir *os.File, name string) (os.FileInfo, error) {
	fd, err := hostFS.Openat(int(dir.Fd()), name, metadataOpenFlags|hostFS.O_NOFOLLOW|hostFS.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	return f.Stat()
}
