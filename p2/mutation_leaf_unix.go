//go:build linux || darwin

package p2

import "os"

func platformMutationLeaf(_ *os.File, leaf string) (string, error) { return leaf, nil }

func platformRenameAt(oldParent *os.File, oldLeaf string, newParent *os.File, newLeaf string) error {
	return hostFS.Renameat(int(oldParent.Fd()), oldLeaf, int(newParent.Fd()), newLeaf)
}

func platformUnlinkAt(parent *os.File, leaf string, flags int) error {
	return hostFS.Unlinkat(int(parent.Fd()), leaf, flags)
}
