//go:build linux || darwin

package core

import "os"

func openPathTimesAt(d *fdEntry, name string) (*os.File, uint64) {
	return openMetadataAt(d, name, true)
}
