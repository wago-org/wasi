package core

import "os"

type pollFile struct {
	file *os.File
	typ  byte
}
