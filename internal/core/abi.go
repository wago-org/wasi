package core

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// WASI Preview 1 errno numbers used by the filesystem implementation.
const (
	wasiEAcces       = 2
	wasiEAgain       = 6
	wasiEExist       = 20
	wasiEFault       = 21
	wasiEIo          = 29
	wasiEIsdir       = 31
	wasiELoop        = 32
	wasiEMfile       = 33
	wasiENametoolong = 37
	wasiENoent       = 44
	wasiENotdir      = 54
	wasiENotempty    = 55
	wasiENotsock     = 57
	wasiEOverflow    = 61
	wasiEPerm        = 63
	wasiERofs        = 69
	wasiENotcapable  = 76
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

func errno(err error) uint64 {
	if err == nil {
		return wasiOK
	}
	switch {
	case errors.Is(err, os.ErrPermission), errors.Is(err, syscall.EACCES):
		return wasiEAcces
	case errors.Is(err, os.ErrNotExist), errors.Is(err, syscall.ENOENT):
		return wasiENoent
	case errors.Is(err, syscall.ENOTEMPTY):
		return wasiENotempty
	case errors.Is(err, os.ErrExist), errors.Is(err, syscall.EEXIST):
		return wasiEExist
	case errors.Is(err, syscall.EBADF):
		return wasiEBadf
	case errors.Is(err, syscall.EINVAL):
		return wasiEInval
	case errors.Is(err, syscall.EISDIR):
		return wasiEIsdir
	case errors.Is(err, syscall.ENOTDIR):
		return wasiENotdir
	case errors.Is(err, syscall.ELOOP):
		return wasiELoop
	case errors.Is(err, syscall.ENAMETOOLONG):
		return wasiENametoolong
	case errors.Is(err, syscall.EROFS):
		return wasiERofs
	case errors.Is(err, syscall.ESPIPE):
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
