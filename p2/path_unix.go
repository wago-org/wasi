//go:build darwin || linux

package p2

import "path"

func readlinkTargetAt(fd int, name string, buf []byte) (int, error) {
	return hostFS.Readlinkat(fd, name, buf)
}

func normalizeSymlinkPath(target string) (string, error) {
	if path.IsAbs(target) {
		return "", hostFS.EPERM
	}
	return target, nil
}
