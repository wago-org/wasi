//go:build darwin || linux

package core

import "syscall"

var hostErrno = platformErrnos{
	EPERM: syscall.EPERM, E2BIG: syscall.E2BIG, EACCES: syscall.EACCES,
	EAGAIN: syscall.EAGAIN, EINTR: syscall.EINTR, ENOSPC: syscall.ENOSPC,
	EDQUOT: syscall.EDQUOT, ENOMEM: syscall.ENOMEM, EMFILE: syscall.EMFILE,
	ENFILE: syscall.ENFILE, EFBIG: syscall.EFBIG, EOVERFLOW: syscall.EOVERFLOW,
	EBUSY: syscall.EBUSY, EPIPE: syscall.EPIPE, EXDEV: syscall.EXDEV,
	ENOENT: syscall.ENOENT, ENOTEMPTY: syscall.ENOTEMPTY, EEXIST: syscall.EEXIST,
	EBADF: syscall.EBADF, EINVAL: syscall.EINVAL, EISDIR: syscall.EISDIR,
	ENOTDIR: syscall.ENOTDIR, ELOOP: syscall.ELOOP, ENAMETOOLONG: syscall.ENAMETOOLONG,
	EROFS: syscall.EROFS, ESPIPE: syscall.ESPIPE,
}

func platformErrno(error) (uint64, bool) { return 0, false }
