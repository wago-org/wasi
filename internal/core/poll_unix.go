//go:build darwin || linux

package core

import (
	"context"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func osFileReady(file *os.File, typ byte) bool {
	events := int16(unix.POLLIN)
	if typ == 2 {
		events = unix.POLLOUT
	}
	fds := []unix.PollFd{{Fd: int32(file.Fd()), Events: events}}
	_, err := unix.Poll(fds, 0)
	return err == nil && fds[0].Revents != 0
}

func waitOSFiles(ctx context.Context, files []pollFile) error {
	fds := make([]unix.PollFd, 0, len(files))
	for _, file := range files {
		events := int16(unix.POLLIN)
		if file.typ == 2 {
			events = unix.POLLOUT
		}
		fds = append(fds, unix.PollFd{Fd: int32(file.file.Fd()), Events: events})
	}
	for {
		ready, err := unix.Poll(fds, 50)
		if err != nil && !errors.Is(err, unix.EINTR) {
			return err
		}
		if ready > 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}
