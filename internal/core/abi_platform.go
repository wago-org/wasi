package core

type platformErrnos struct {
	EPERM, E2BIG, EACCES, EAGAIN, EINTR, ENOSPC, EDQUOT, ENOMEM   error
	EMFILE, ENFILE, EFBIG, EOVERFLOW, EBUSY, EPIPE, EXDEV, ENOENT error
	ENOTEMPTY, EEXIST, EBADF, EINVAL, EISDIR, ENOTDIR, ELOOP      error
	ENAMETOOLONG, EROFS, ESPIPE                                   error
}
