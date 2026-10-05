//go:build windows

package p2

import (
	"os"
	"path"
	"strings"

	"github.com/wago-org/wasi/internal/winfs"
	"golang.org/x/sys/windows"
)

func platformMutationLeaf(_ *os.File, leaf string) (string, error) {
	// Legacy winfs callers accept a single leaf name. Unlink and rename use
	// the raw leaf with their own atomic directory-requirement checks.
	return path.Clean(leaf), nil
}

func platformRenameAt(oldParent *os.File, oldLeaf string, newParent *os.File, newLeaf string) error {
	oldDirectory := strings.HasSuffix(oldLeaf, "/") || strings.HasSuffix(oldLeaf, "/.")
	newDirectory := strings.HasSuffix(newLeaf, "/") || strings.HasSuffix(newLeaf, "/.")
	return winfs.RenameAtDirectories(windows.Handle(oldParent.Fd()), path.Clean(oldLeaf),
		windows.Handle(newParent.Fd()), path.Clean(newLeaf), oldDirectory, newDirectory)
}

func platformUnlinkAt(parent *os.File, leaf string, flags int) error {
	requireDirectory := strings.HasSuffix(leaf, "/") || strings.HasSuffix(leaf, "/.")
	name := path.Clean(leaf)
	if requireDirectory {
		return winfs.DeleteAtDirectoryRequired(windows.Handle(parent.Fd()), name, flags != 0)
	}
	return winfs.DeleteAt(windows.Handle(parent.Fd()), name, flags != 0)
}
