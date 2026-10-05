//go:build windows

package core

import "os"

// Windows path_open rejects NONBLOCK before opening a descriptor.
func readNonblocking(file *os.File, b []byte) (int, error)  { return file.Read(b) }
func writeNonblocking(file *os.File, b []byte) (int, error) { return file.Write(b) }
