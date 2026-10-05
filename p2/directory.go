package p2

import (
	"errors"
	"io"

	component "github.com/wago-org/component-model"
)

func (d *directoryStream) readEntry(maxNameBytes uint64) []component.Value {
	d.mu.Lock()
	defer d.mu.Unlock()
	name, kind, readErr := readDirectoryEntry(d, maxNameBytes)
	if readErr != nil {
		if errors.Is(readErr, io.EOF) {
			return ok(nil)
		}
		return fsFailure(readErr)
	}
	if name == "" {
		return ok(nil)
	}
	if uint64(len(name)) > maxNameBytes {
		return fsFailure(hostFS.ENAMETOOLONG)
	}
	return ok([]component.Value{kind, name})
}
