//go:build darwin || linux

package p2

import (
	"os"

	sysunix "golang.org/x/sys/unix"
)

func openPreopenDirectory(path string) (*os.File, error) { return os.Open(path) }

func platformFilesystemError(error) (uint32, bool) { return 0, false }

var hostFS = filesystemPlatform{
	EROFS: sysunix.EROFS, EINVAL: sysunix.EINVAL, EOVERFLOW: sysunix.EOVERFLOW, EMFILE: sysunix.EMFILE,
	EPERM: sysunix.EPERM, ENOENT: sysunix.ENOENT, EACCES: sysunix.EACCES, EAGAIN: sysunix.EAGAIN,
	EALREADY: sysunix.EALREADY, EBADF: sysunix.EBADF, EBUSY: sysunix.EBUSY, EDEADLK: sysunix.EDEADLK,
	EDQUOT: sysunix.EDQUOT, EEXIST: sysunix.EEXIST, EFBIG: sysunix.EFBIG, EILSEQ: sysunix.EILSEQ,
	EINPROGRESS: sysunix.EINPROGRESS, EINTR: sysunix.EINTR, EISDIR: sysunix.EISDIR, ENOTDIR: sysunix.ENOTDIR,
	ENOTEMPTY: sysunix.ENOTEMPTY, ELOOP: sysunix.ELOOP, ENAMETOOLONG: sysunix.ENAMETOOLONG,
	ENODEV: sysunix.ENODEV, ENOLCK: sysunix.ENOLCK, ENOMEM: sysunix.ENOMEM, ENOSPC: sysunix.ENOSPC,
	ENOTRECOVERABLE: sysunix.ENOTRECOVERABLE, ENOTSUP: sysunix.ENOTSUP, ENOSYS: sysunix.ENOSYS,
	ENOTTY: sysunix.ENOTTY, ENXIO: sysunix.ENXIO, EXDEV: sysunix.EXDEV, EPIPE: sysunix.EPIPE,
	ESPIPE: sysunix.ESPIPE, ETXTBSY: sysunix.ETXTBSY, ENFILE: sysunix.ENFILE,
	O_RDONLY: sysunix.O_RDONLY, O_RDWR: sysunix.O_RDWR, O_WRONLY: sysunix.O_WRONLY,
	O_DIRECTORY: sysunix.O_DIRECTORY, O_CREAT: sysunix.O_CREAT, O_EXCL: sysunix.O_EXCL,
	O_TRUNC: sysunix.O_TRUNC, O_NOFOLLOW: sysunix.O_NOFOLLOW, O_CLOEXEC: sysunix.O_CLOEXEC,
	AT_REMOVEDIR: sysunix.AT_REMOVEDIR,
	Dup:          sysunix.Dup, Openat: sysunix.Openat, Mkdirat: sysunix.Mkdirat, Unlinkat: sysunix.Unlinkat,
	Renameat: sysunix.Renameat, Linkat: sysunix.Linkat, Readlinkat: sysunix.Readlinkat,
	Symlinkat: sysunix.Symlinkat,
}
