//go:build linux || darwin

package p2

import sysunix "golang.org/x/sys/unix"

func statDirectoryEntry(fd int, name string, stat *sysunix.Stat_t) error {
	for {
		err := sysunix.Fstatat(fd, name, stat, sysunix.AT_SYMLINK_NOFOLLOW)
		if err != sysunix.EINTR {
			return err
		}
	}
}

func directoryStatKind(mode uint32) uint32 {
	switch mode & sysunix.S_IFMT {
	case sysunix.S_IFBLK:
		return descriptorBlockDevice
	case sysunix.S_IFCHR:
		return descriptorCharacterDevice
	case sysunix.S_IFDIR:
		return descriptorDirectory
	case sysunix.S_IFIFO:
		return descriptorFIFO
	case sysunix.S_IFLNK:
		return descriptorSymbolicLink
	case sysunix.S_IFREG:
		return descriptorRegularFile
	case sysunix.S_IFSOCK:
		return descriptorSocket
	default:
		return descriptorUnknown
	}
}

func nativeDirectoryType(typ uint8) (uint32, bool) {
	switch typ {
	case sysunix.DT_BLK:
		return descriptorBlockDevice, true
	case sysunix.DT_CHR:
		return descriptorCharacterDevice, true
	case sysunix.DT_DIR:
		return descriptorDirectory, true
	case sysunix.DT_FIFO:
		return descriptorFIFO, true
	case sysunix.DT_LNK:
		return descriptorSymbolicLink, true
	case sysunix.DT_REG:
		return descriptorRegularFile, true
	case sysunix.DT_SOCK:
		return descriptorSocket, true
	default:
		return descriptorUnknown, false
	}
}
