package p2

import (
	"errors"
	"io"

	component "github.com/wago-org/component-model"
)

func (d *directoryStream) readEntry(maxNameBytes uint64) []component.Value {
	d.mu.Lock()
	defer d.mu.Unlock()
	entries, readErr := d.file.ReadDir(1)
	if readErr != nil {
		if errors.Is(readErr, io.EOF) {
			return ok(nil)
		}
		return fsFailure(readErr)
	}
	if len(entries) == 0 {
		return ok(nil)
	}
	entry := entries[0]
	if uint64(len(entry.Name())) > maxNameBytes {
		return fsFailure(hostFS.ENAMETOOLONG)
	}
	// ReadDir resolves entry types even when the filesystem omits them.
	return ok([]component.Value{descriptorModeKind(entry.Type()), entry.Name()})
}
