//go:build linux

package p2_test

import (
	"context"
	_ "embed"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	component "github.com/wago-org/component-model"
	"github.com/wago-org/wasi/p2"
)

//go:embed testdata/initialization_failure/component.wasm
var initializationFailureComponent []byte

func TestP2RunInitializationFailureClosesPreopen(t *testing.T) {
	const childMarker = "WASI_P2_INITIALIZATION_CLEANUP_CHILD"
	if os.Getenv(childMarker) == "1" {
		_, ref := optionsTestService(t)
		root := t.TempDir()
		countMountHandles := func() int {
			entries, err := os.ReadDir("/proc/self/fd")
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, entry := range entries {
				target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
				if err == nil && target == root {
					count++
				}
			}
			return count
		}
		before := countMountHandles()
		err := ref.With(func(service component.Service) error {
			return p2.Run(context.Background(), service, initializationFailureComponent, p2.Config{
				Mounts: []p2.Preopen{{GuestPath: "/data", HostPath: root, Read: true}},
			})
		})
		if err == nil || !strings.Contains(err.Error(), "unreachable") {
			t.Fatalf("initialization error = %v, want fixture setup failure", err)
		}
		if after := countMountHandles(); after != before {
			t.Fatalf("failed initialization leaves %d mount handles, want %d", after, before)
		}
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestP2RunInitializationFailureClosesPreopen$", "-test.count=1")
	command.Env = append(os.Environ(), childMarker+"=1")
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("initialization cleanup check exceeded five seconds: %v\n%s", ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("initialization cleanup check: %v\n%s", err, output)
	}
}
