//go:build linux || darwin

package p2

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	component "github.com/wago-org/component-model"
	"golang.org/x/sys/unix"
)

func TestSetTimesUnderPathFlagsDoesNotWaitForFIFOWriter(t *testing.T) {
	const helperRoot = "WASI_P2_SET_TIMES_FIFO_TEST_ROOT"
	if root := os.Getenv(helperRoot); root != "" {
		base, err := openPreopenDirectory(root, true)
		if err != nil {
			t.Fatal(err)
		}
		defer base.Close()
		want := time.Date(2007, time.August, 9, 10, 11, 12, 0, time.UTC)
		if err := setTimesUnderPathFlags(base, "pipe", 0, component.VariantValue{Disc: 0}, timestampForTest(want), time.Now); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(root + "/pipe")
		if err != nil || info.Mode()&os.ModeNamedPipe == 0 || !info.ModTime().Equal(want) {
			t.Fatalf("FIFO timestamps = %v, %v", info, err)
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestSetTimesUnderPathFlagsDoesNotWaitForFIFOWriter$")
	cmd.Env = append(os.Environ(), helperRoot+"="+root)
	cmd.WaitDelay = time.Second
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("FIFO timestamp update exceeded its deadline: %v\n%s", ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("FIFO timestamp subprocess: %v\n%s", err, output)
	}
}
