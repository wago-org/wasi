//go:build darwin || linux

package core

import (
	"context"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func osFileReady(file *os.File, typ byte) bool {
	conn, err := file.SyscallConn()
	if err != nil {
		return false
	}
	events := int16(unix.POLLIN)
	if typ == 2 {
		events = unix.POLLOUT
	}
	var ready bool
	if err := conn.Control(func(fd uintptr) {
		fds := []unix.PollFd{{Fd: int32(fd), Events: events}}
		_, pollErr := unix.Poll(fds, 0)
		ready = pollErr == nil && fds[0].Revents != 0
	}); err != nil {
		return false
	}
	return ready
}

func osFileError(file *os.File) uint16 {
	conn, err := file.SyscallConn()
	if err != nil {
		return wasiEBadf
	}
	var stat unix.Stat_t
	var statErr error
	if err := conn.Control(func(fd uintptr) {
		statErr = unix.Fstat(int(fd), &stat)
	}); err != nil {
		return wasiEBadf
	}
	return uint16(errno(statErr))
}

func waitOSFiles(ctx context.Context, files []pollFile) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, file := range files {
			if osFileError(file.file) != 0 || osFileReady(file.file, file.typ) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
