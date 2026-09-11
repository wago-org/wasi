package core

import (
	"errors"
	"io/fs"
	"os"
)

// WASI Preview 1 errno numbers used by the filesystem implementation.
const (
	wasiE2big        = 1
	wasiEAcces       = 2
	wasiEAgain       = 6
	wasiEBusy        = 10
	wasiEDquot       = 19
	wasiEExist       = 20
	wasiEFault       = 21
	wasiEFbig        = 22
	wasiEIntr        = 27
	wasiEIo          = 29
	wasiEIsdir       = 31
	wasiELoop        = 32
	wasiEMfile       = 33
	wasiENametoolong = 37
	wasiENfile       = 41
	wasiENoent       = 44
	wasiENomem       = 48
	wasiENospc       = 51
	wasiENotdir      = 54
	wasiENotempty    = 55
	wasiENotsock     = 57
	wasiEOverflow    = 61
	wasiEPerm        = 63
	wasiEPipe        = 64
	wasiERofs        = 69
	wasiENotcapable  = 76
	wasiEXdev        = 75
)

const (
	filetypeUnknown = iota
	filetypeBlockDevice
	filetypeCharacterDevice
	filetypeDirectory
	filetypeRegularFile
	filetypeSocketDgram
	filetypeSocketStream
	filetypeSymlink
)

const (
	rightFDDataSync uint64 = 1 << iota
	rightFDRead
	rightFDSeek
	rightFDStatSetFlags
	rightFDSync
	rightFDTell
	rightFDWrite
	rightFDAdvise
	rightFDAllocate
	rightPathCreateDirectory
	rightPathCreateFile
	rightPathLinkSource
	rightPathLinkTarget
	rightPathOpen
	rightFDReadDir
	rightPathReadlink
	rightPathRenameSource
	rightPathRenameTarget
	rightPathFilestatGet
	rightPathFilestatSetSize
	rightPathFilestatSetTimes
	rightFDFilestatGet
	rightFDFilestatSetSize
	rightFDFilestatSetTimes
	rightPathSymlink
	rightPathRemoveDirectory
	rightPathUnlinkFile
	rightPollFDReadWrite
	rightSockShutdown
)

const allRights = (uint64(1) << 29) - 1

const directoryRights = rightPathCreateDirectory | rightPathCreateFile |
	rightPathLinkSource | rightPathLinkTarget | rightPathOpen | rightFDReadDir |
	rightPathReadlink | rightPathRenameSource | rightPathRenameTarget |
	rightPathFilestatGet | rightPathFilestatSetSize | rightPathFilestatSetTimes | rightFDFilestatGet |
	rightFDFilestatSetTimes | rightPathSymlink | rightPathRemoveDirectory |
	rightPathUnlinkFile

const directoryReadRights = rightPathOpen | rightFDReadDir | rightPathReadlink |
	rightPathFilestatGet | rightFDFilestatGet | rightPollFDReadWrite

const fileReadRights = rightFDRead | rightFDSeek | rightFDTell | rightFDAdvise |
	rightFDFilestatGet | rightPollFDReadWrite

const fileWriteRights = rightFDWrite | rightFDDataSync | rightFDSync |
	rightFDStatSetFlags | rightFDAllocate | rightFDFilestatSetSize |
	rightFDFilestatSetTimes | rightPollFDReadWrite

const directoryMutationRights = rightPathCreateDirectory | rightPathCreateFile |
	rightPathLinkSource | rightPathLinkTarget | rightPathRenameSource |
	rightPathRenameTarget | rightPathSymlink | rightPathRemoveDirectory |
	rightPathUnlinkFile

func errno(err error) uint64 {
	if err == nil {
		return wasiOK
	}
	if code, ok := platformErrno(err); ok {
		return code
	}
	switch {
	case errors.Is(err, hostErrno.EPERM):
		return wasiEPerm
	case errors.Is(err, hostErrno.E2BIG):
		return wasiE2big
	case errors.Is(err, os.ErrPermission), errors.Is(err, hostErrno.EACCES):
		return wasiEAcces
	case errors.Is(err, hostErrno.EAGAIN):
		return wasiEAgain
	case errors.Is(err, hostErrno.EINTR):
		return wasiEIntr
	case errors.Is(err, hostErrno.ENOSPC):
		return wasiENospc
	case errors.Is(err, hostErrno.EDQUOT):
		return wasiEDquot
	case errors.Is(err, hostErrno.ENOMEM):
		return wasiENomem
	case errors.Is(err, hostErrno.EMFILE):
		return wasiEMfile
	case errors.Is(err, hostErrno.ENFILE):
		return wasiENfile
	case errors.Is(err, hostErrno.EFBIG):
		return wasiEFbig
	case errors.Is(err, hostErrno.EOVERFLOW):
		return wasiEOverflow
	case errors.Is(err, hostErrno.EBUSY):
		return wasiEBusy
	case errors.Is(err, hostErrno.EPIPE):
		return wasiEPipe
	case errors.Is(err, hostErrno.EXDEV):
		return wasiEXdev
	case errors.Is(err, os.ErrNotExist), errors.Is(err, hostErrno.ENOENT):
		return wasiENoent
	case errors.Is(err, hostErrno.ENOTEMPTY):
		return wasiENotempty
	case errors.Is(err, os.ErrExist), errors.Is(err, hostErrno.EEXIST):
		return wasiEExist
	case errors.Is(err, hostErrno.EBADF):
		return wasiEBadf
	case errors.Is(err, hostErrno.EINVAL):
		return wasiEInval
	case errors.Is(err, hostErrno.EISDIR):
		return wasiEIsdir
	case errors.Is(err, hostErrno.ENOTDIR):
		return wasiENotdir
	case errors.Is(err, hostErrno.ELOOP):
		return wasiELoop
	case errors.Is(err, hostErrno.ENAMETOOLONG):
		return wasiENametoolong
	case errors.Is(err, hostErrno.EROFS):
		return wasiERofs
	case errors.Is(err, hostErrno.ESPIPE):
		return wasiESpipe
	default:
		return wasiEIo
	}
}

func filetype(info fs.FileInfo) byte {
	mode := info.Mode()
	switch {
	case mode.IsDir():
		return filetypeDirectory
	case mode.IsRegular():
		return filetypeRegularFile
	case mode&os.ModeSymlink != 0:
		return filetypeSymlink
	case mode&os.ModeCharDevice != 0:
		return filetypeCharacterDevice
	case mode&os.ModeDevice != 0:
		return filetypeBlockDevice
	case mode&os.ModeSocket != 0:
		return filetypeSocketStream
	default:
		return filetypeUnknown
	}
}
