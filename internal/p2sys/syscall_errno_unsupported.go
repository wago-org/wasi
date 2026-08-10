//go:build plan9 || aix

package p2sys

func syscallToErrno(err error) (Errno, bool) {
	return 0, false
}
