//go:build darwin || windows

package p2

import "os"

func platformOpenUnderPathFlags(dir *os.File, name string, flags int, mode uint32, follow bool) (*os.File, error) {
	return openUnderWalk(dir, name, flags, mode, follow)
}
