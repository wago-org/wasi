//go:build darwin || linux

package core

import (
	"context"
	"os"

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
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		fds := make([]unix.PollFd, 0, len(files))
		for _, file := range files {
			conn, err := file.file.SyscallConn()
			if err != nil {
				// Let readySubscriptions report the descriptor error.
				return nil
			}
			events := int16(unix.POLLIN)
			if file.typ == 2 {
				events = unix.POLLOUT
			}
			if err := conn.Control(func(fd uintptr) {
				fds = append(fds, unix.PollFd{Fd: int32(fd), Events: events})
			}); err != nil {
				return nil
			}
		}
		// Reacquire each descriptor after this bounded wait. A closed fd may be
		// reused by an unrelated file before poll returns.
		n, err := unix.Poll(fds, 50)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		if n != 0 {
			return nil
		}
	}
}
