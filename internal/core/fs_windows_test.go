//go:build windows

package core

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsOpenAtUsesDescriptorDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "file"), []byte("nested"), 0o600); err != nil {
		t.Fatal(err)
	}
	preopen, err := openPreopen(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer preopen.Close()
	canonical, err := hostMountRoot(preopen, root)
	if err != nil {
		t.Fatal(err)
	}
	dir, code := openAt(&fdEntry{file: preopen, mount: root, root: canonical}, "sub", hostOpenReadOnly|hostOpenDirectory, 0)
	if code != wasiOK {
		t.Fatalf("open subdirectory: errno %d", code)
	}
	defer dir.Close()
	file, code := openAt(&fdEntry{file: dir, mount: root, root: canonical}, "file", hostOpenReadOnly, 0)
	if code != wasiOK {
		t.Fatalf("open nested file: errno %d", code)
	}
	defer file.Close()
	got, err := io.ReadAll(file)
	if err != nil || string(got) != "nested" {
		t.Fatalf("nested file = %q, %v", got, err)
	}
	metadata, code := openMetadataAt(&fdEntry{file: dir, mount: root, root: canonical}, "file", false)
	if code != wasiOK {
		t.Fatalf("open nested metadata: errno %d", code)
	}
	if _, err := metadata.Stat(); err != nil {
		metadata.Close()
		t.Fatalf("stat nested metadata: %v", err)
	}
	metadata.Close()
	directory, code := openMetadataAt(&fdEntry{file: preopen, mount: root, root: canonical}, "sub", true)
	if code != wasiOK {
		t.Fatalf("open directory metadata: errno %d", code)
	}
	if info, err := directory.Stat(); err != nil || !info.IsDir() {
		directory.Close()
		t.Fatalf("stat directory metadata = %#v, %v", info, err)
	}
	directory.Close()
}

func TestWindowsSetPathTimesNoFollowUpdatesSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink("target", link); err != nil {
		t.Fatal(err)
	}
	preopen, err := openPreopen(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer preopen.Close()
	want := time.Date(2001, time.February, 3, 4, 5, 6, 0, time.UTC)
	if err := setPathTimes(preopen, "link", []time.Time{want, want}, true); err != nil {
		t.Fatal(err)
	}
	linkInfo, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if !linkInfo.ModTime().Equal(want) {
		t.Fatalf("link mtime = %v, want %v", linkInfo.ModTime(), want)
	}
	targetInfo, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if targetInfo.ModTime().Equal(want) {
		t.Fatal("no-follow timestamp update changed symlink target")
	}
}

func TestWindowsSetPathTimesUpdatesPreopenDirectory(t *testing.T) {
	root := t.TempDir()
	preopen, err := openPreopen(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer preopen.Close()
	want := time.Date(2002, time.March, 4, 5, 6, 7, 0, time.UTC)
	if err := setPathTimes(preopen, ".", []time.Time{want, want}, false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(want) {
		t.Fatalf("preopen mtime = %v, want %v", info.ModTime(), want)
	}
}

func TestWindowsOpenAtAcceptsSymlinkPreopen(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "file"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	preopen, err := openPreopen(link, true)
	if err != nil {
		t.Fatal(err)
	}
	defer preopen.Close()
	canonical, err := hostMountRoot(preopen, link)
	if err != nil {
		t.Fatal(err)
	}
	file, code := openAt(&fdEntry{file: preopen, mount: link, root: canonical}, "file", hostOpenReadOnly, 0)
	if code != wasiOK {
		t.Fatalf("open through symlink preopen: errno %d", code)
	}
	file.Close()
}

func TestWindowsOpenAtCreateAppendIsAppendOnly(t *testing.T) {
	root := t.TempDir()
	preopen, err := openPreopen(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer preopen.Close()
	canonical, err := hostMountRoot(preopen, root)
	if err != nil {
		t.Fatal(err)
	}
	file, code := openAt(&fdEntry{file: preopen, mount: root, root: canonical}, "append", os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if code != wasiOK {
		t.Fatalf("create append: errno %d", code)
	}
	defer file.Close()
	if _, err := file.WriteString("A"); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("B"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "append"))
	if err != nil || string(got) != "AB" {
		t.Fatalf("file = %q, %v", got, err)
	}
}

func TestWindowsSetAppendFlagReopensSameFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "append")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	entry := &fdEntry{file: file, rights: rightFDRead | rightFDWrite}
	t.Cleanup(func() { _ = entry.file.Close() })
	if _, err := entry.file.WriteString("A"); err != nil {
		t.Fatal(err)
	}
	if _, err := entry.file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if err := setHostFileFlags(entry, entry.file, 1); err != nil {
		t.Fatal(err)
	}
	entry.flags = 1
	if _, err := entry.file.WriteString("B"); err != nil {
		t.Fatal(err)
	}
	if err := setHostFileFlags(entry, entry.file, 0); err != nil {
		t.Fatal(err)
	}
	entry.flags = 0
	if _, err := entry.file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := entry.file.WriteString("C"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "CB" {
		t.Fatalf("file = %q, %v", got, err)
	}
}
