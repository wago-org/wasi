//go:build !windows

package p2

import (
	"io/fs"
	"os"
)

func statFile(f *os.File) (fs.FileInfo, error) { return f.Stat() }
