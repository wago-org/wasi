//go:build linux

package p2

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestStatUnderDoesNotWaitForFIFOWriter(t *testing.T) {
	const helperRoot = "WASI_P2_FIFO_METADATA_TEST_ROOT"
	if root := os.Getenv(helperRoot); root != "" {
		dir, err := os.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		defer dir.Close()
		info, err := statUnder(dir, "pipe")
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeNamedPipe == 0 {
			t.Fatalf("FIFO stat mode = %v", info.Mode())
		}
		return
	}

	root := t.TempDir()
	if err := unix.Mkfifo(root+"/pipe", 0o600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// A subprocess lets a deadline kill a blocked open without needing a FIFO
	// writer, which could itself block if the reader finishes at the deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestStatUnderDoesNotWaitForFIFOWriter$")
	cmd.Env = append(os.Environ(), helperRoot+"="+root)
	cmd.WaitDelay = time.Second
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("FIFO metadata lookup exceeded its deadline: %v\n%s", ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("FIFO metadata subprocess: %v\n%s", err, output)
	}
}
