//go:build !windows

package p2sys

func errorToErrno(err error) Errno {
	if errno, ok := err.(Errno); ok {
		return errno
	}
	if errno, ok := syscallToErrno(err); ok {
		return errno
	}
	return EIO
}
