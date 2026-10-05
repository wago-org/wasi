//go:build darwin || linux

package core

import (
	"golang.org/x/sys/unix"
	"os"
)

// Go's os.File methods wait through the runtime poller on EAGAIN. WASI's
// NONBLOCK descriptors must return EAGAIN directly instead.
func readNonblocking(file *os.File, b []byte) (int, error) {
	for {
		n, err := unix.Read(int(file.Fd()), b)
		if err == unix.EINTR {
			continue
		}
		if n < 0 {
			n = 0
		}
		return n, err
	}
}

func writeNonblocking(file *os.File, b []byte) (int, error) {
	for {
		n, err := unix.Write(int(file.Fd()), b)
		if err == unix.EINTR {
			continue
		}
		if n < 0 {
			n = 0
		}
		return n, err
	}
}
