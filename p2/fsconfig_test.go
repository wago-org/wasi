package p2

import (
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	sys "github.com/wago-org/wasi/internal/p2sys"
)

func TestFSConfigDirMountIsImmutableAndNormalizesGuestPath(t *testing.T) {
	root := t.TempDir()
	base := NewFSConfig()
	mounted := base.WithDirMount(root, "/tmp/")

	if got := fsMountsFromConfig(base); len(got) != 0 {
		t.Fatalf("base config changed: %d mounts", len(got))
	}
	mounts := fsMountsFromConfig(mounted)
	if len(mounts) != 1 || mounts[0].guestPath != "/tmp" {
		t.Fatalf("mounts = %#v, want one mount at /tmp", mounts)
	}
	if _, errno := mounts[0].fs.OpenFile("../escape", 0, 0); errno == 0 {
		t.Fatal("mount allowed parent traversal")
	}
}

func TestFSConfigDirMountReadsAndWritesInsideRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "input"), []byte("in"), 0o600); err != nil {
		t.Fatal(err)
	}
	mount := fsMountsFromConfig(NewFSConfig().WithDirMount(root, "/"))[0]
	f, errno := mount.fs.OpenFile("input", 0, 0)
	if errno != 0 {
		t.Fatalf("open input: %v", errno)
	}
	defer f.Close()
	buf := make([]byte, 2)
	if n, errno := f.Read(buf); errno != 0 || n != 2 || string(buf) != "in" {
		t.Fatalf("read = %q, %d, %v", buf, n, errno)
	}
}

func TestFSConfigDirMountRejectsSymlinkEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	mount := fsMountsFromConfig(NewFSConfig().WithDirMount(root, "/"))[0]
	if _, errno := mount.fs.OpenFile("escape/secret", 0, 0); errno == 0 {
		t.Fatal("mount followed a symlink outside its host root")
	}
}

func TestFSConfigDirMountHonorsFinalSymlinkFollow(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	mount := fsMountsFromConfig(NewFSConfig().WithDirMount(root, "/"))[0]

	f, errno := mount.fs.OpenFile("link", sys.O_RDONLY, 0)
	if errno != 0 {
		t.Fatalf("follow final symlink: %v", errno)
	}
	f.Close()
	if _, errno = mount.fs.OpenFile("link", sys.O_RDONLY|sys.O_NOFOLLOW, 0); errno == 0 {
		t.Fatal("open with O_NOFOLLOW followed a final symlink")
	}
	st, errno := mount.fs.Lstat("link")
	if errno != 0 || st.Mode&fs.ModeSymlink == 0 {
		t.Fatalf("lstat(link) = mode %v, errno %v", st.Mode, errno)
	}
}

func TestFSConfigDirMountAllowsOnlyConfinedIntermediateSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "inside"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inside", "file"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("inside", filepath.Join(root, "safe")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	mount := fsMountsFromConfig(NewFSConfig().WithDirMount(root, "/"))[0]
	if f, errno := mount.fs.OpenFile("safe/file", sys.O_RDONLY, 0); errno != 0 {
		t.Fatalf("confined intermediate symlink: %v", errno)
	} else {
		f.Close()
	}
	if _, errno := mount.fs.OpenFile("escape/file", sys.O_RDONLY, 0); errno == 0 {
		t.Fatal("outward intermediate symlink was followed")
	}
}

func TestFSConfigSerializesPathMutationWithResolution(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "a"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "file"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	mount := fsMountsFromConfig(NewFSConfig().WithDirMount(root, "/"))[0]
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if f, errno := mount.fs.OpenFile("a/file", sys.O_RDONLY, 0); errno == 0 {
				f.Close()
			}
		}()
		go func() {
			defer wg.Done()
			if errno := mount.fs.Rename("a", "b"); errno == 0 {
				_ = mount.fs.Rename("b", "a")
			}
		}()
	}
	wg.Wait()
}
