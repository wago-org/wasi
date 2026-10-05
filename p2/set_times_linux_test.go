//go:build linux

package p2

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	component "github.com/wago-org/component-model"
	"golang.org/x/sys/unix"
)

func TestSetTimesUnderPathFlagsWithoutEmptyPath(t *testing.T) {
	for _, unavailable := range []error{unix.EINVAL, unix.ENOENT, unix.ENOSYS} {
		for _, tc := range []struct {
			name   string
			path   string
			follow bool
			object string
		}{
			{"ordinary file", "file", false, "file"},
			{"ordinary file follow", "file", true, "file"},
			{"link itself", "link", false, "link"},
			{"link target", "link", true, "file"},
			{"directory trailing slash", "directory/", false, "directory"},
			{"directory link trailing slash", "dirlink/", false, "directory"},
			{"base directory", ".", false, "."},
		} {
			t.Run(unavailable.Error()+"/"+tc.name, func(t *testing.T) {
				root := t.TempDir()
				if err := os.WriteFile(filepath.Join(root, "file"), []byte("data"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("file", filepath.Join(root, "link")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("directory", filepath.Join(root, "dirlink")); err != nil {
					t.Fatal(err)
				}
				base, err := openPreopenDirectory(root, true)
				if err != nil {
					t.Fatal(err)
				}
				defer base.Close()
				beforeFile, err := os.Lstat(filepath.Join(root, "file"))
				if err != nil {
					t.Fatal(err)
				}
				beforeLink, err := os.Lstat(filepath.Join(root, "link"))
				if err != nil {
					t.Fatal(err)
				}
				attempts := 0
				updateTimes := func(f *os.File, _, _ time.Time) error {
					attempts++
					flags, err := unix.FcntlInt(f.Fd(), unix.F_GETFL, 0)
					if err != nil || flags&unix.O_PATH == 0 {
						t.Fatalf("initial timestamp handle flags = %x, %v, want O_PATH", flags, err)
					}
					return unavailable
				}
				want := time.Date(2013, time.February, 15, 16, 17, 18, 123456789, time.UTC)
				if err := setTimesUnderPathFlagsLinux(base, tc.path, tc.follow, component.VariantValue{Disc: 0}, timestampForTest(want), time.Now, updateTimes); err != nil {
					t.Fatal(err)
				}
				if attempts != 1 {
					t.Fatalf("AT_EMPTY_PATH attempts = %d, want 1", attempts)
				}
				info, err := os.Lstat(filepath.Join(root, tc.object))
				if err != nil || !info.ModTime().Equal(want) {
					t.Fatalf("fallback object timestamps = %v, %v, want %v", info, err, want)
				}
				for _, other := range []struct {
					name string
					info os.FileInfo
				}{{"file", beforeFile}, {"link", beforeLink}} {
					if tc.object == other.name {
						continue
					}
					after, err := os.Lstat(filepath.Join(root, other.name))
					if err != nil || !after.ModTime().Equal(other.info.ModTime()) {
						t.Fatalf("unselected %s changed: %v, %v", other.name, after, err)
					}
				}
			})
		}
	}
}

func TestSetTimesUnderPathFlagsDoesNotRetryPermissionError(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := openPreopenDirectory(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	before, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	want := timestampForTest(time.Date(2013, time.February, 15, 16, 17, 18, 0, time.UTC))
	err = setTimesUnderPathFlagsLinux(base, "file", false, want, want, time.Now, func(*os.File, time.Time, time.Time) error { return unix.EACCES })
	if !errors.Is(err, unix.EACCES) {
		t.Fatalf("permission failure = %v, want access denied", err)
	}
	after, err := os.Stat(file)
	if err != nil || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("failed mutation changed timestamps: %v, %v", after, err)
	}
}

func TestSetTimesUnderPathFlagsWithoutEmptyPathDoesNotWaitForFIFOWriter(t *testing.T) {
	const helperRoot = "WASI_P2_SET_TIMES_OLD_KERNEL_FIFO_ROOT"
	if root := os.Getenv(helperRoot); root != "" {
		base, err := openPreopenDirectory(root, true)
		if err != nil {
			t.Fatal(err)
		}
		defer base.Close()
		want := time.Date(2013, time.February, 15, 16, 17, 18, 0, time.UTC)
		attempts := 0
		err = setTimesUnderPathFlagsLinux(base, "pipe", false, timestampForTest(want), timestampForTest(want), time.Now, func(*os.File, time.Time, time.Time) error {
			attempts++
			return unix.ENOSYS
		})
		if err != nil || attempts != 1 {
			t.Fatalf("fallback FIFO update = %v, attempts %d", err, attempts)
		}
		info, err := os.Lstat(filepath.Join(root, "pipe"))
		if err != nil || info.Mode()&os.ModeNamedPipe == 0 || !info.ModTime().Equal(want) {
			t.Fatalf("fallback FIFO timestamps = %v, %v", info, err)
		}
		return
	}
	root := t.TempDir()
	if err := unix.Mkfifo(filepath.Join(root, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestSetTimesUnderPathFlagsWithoutEmptyPathDoesNotWaitForFIFOWriter$")
	cmd.Env = append(os.Environ(), helperRoot+"="+root)
	cmd.WaitDelay = time.Second
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("old-kernel FIFO update exceeded its deadline: %v\n%s", ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("old-kernel FIFO timestamp subprocess: %v\n%s", err, output)
	}
}
