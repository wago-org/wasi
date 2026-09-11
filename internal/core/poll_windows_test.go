//go:build windows

package core

import (
	"os"
	"testing"
)

func TestWindowsPipeReadiness(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if !osFileReady(writer, 2) {
		t.Fatal("empty pipe is not writable")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if !osFileReady(reader, 1) {
		t.Fatal("pipe EOF is not readable")
	}
}
