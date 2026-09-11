package p2

type filesystemPlatform struct {
	EROFS, EINVAL, EOVERFLOW, EMFILE, EPERM, ENOENT, EACCES           error
	EAGAIN, EALREADY, EBADF, EBUSY, EDEADLK, EDQUOT, EEXIST           error
	EFBIG, EILSEQ, EINPROGRESS, EINTR, EISDIR, ENOTDIR, ENOTEMPTY     error
	ELOOP, ENAMETOOLONG, ENODEV, ENOLCK, ENOMEM, ENOSPC               error
	ENOTRECOVERABLE, ENOTSUP, ENOSYS, ENOTTY, ENXIO, EXDEV            error
	EPIPE, ESPIPE, ETXTBSY, ENFILE                                    error
	O_RDONLY, O_RDWR, O_WRONLY, O_DIRECTORY, O_CREAT, O_EXCL, O_TRUNC int
	O_NOFOLLOW, O_CLOEXEC, AT_REMOVEDIR                               int
	Dup                                                               func(int) (int, error)
	Openat                                                            func(int, string, int, uint32) (int, error)
	Mkdirat                                                           func(int, string, uint32) error
	Unlinkat                                                          func(int, string, int) error
	Renameat                                                          func(int, string, int, string) error
	Linkat                                                            func(int, string, int, string, int) error
	Readlinkat                                                        func(int, string, []byte) (int, error)
	Symlinkat                                                         func(string, int, string) error
}
