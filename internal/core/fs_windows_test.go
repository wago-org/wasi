//go:build windows

package core

import (
	"encoding/binary"
	"fmt"
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

func TestWindowsPathOpenCannotCreateDirectoryWithoutDirectoryRight(t *testing.T) {
	root := t.TempDir()
	e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true, MutateDirectory: true}}})
	e.fs.fds[3].rights &^= rightPathCreateDirectory
	m := testModule{mem: make([]byte, 128)}
	copy(m.mem[32:], "created")
	r := make([]uint64, 1)
	e.pathOpen(m, []uint64{3, 0, 32, 7, 3, rightFDRead, 0, 0, 16}, r)
	if r[0] != wasiENotcapable {
		t.Fatalf("path_open CREAT|DIRECTORY without directory right = errno %d, want ENOTCAPABLE", r[0])
	}
	if _, err := os.Stat(filepath.Join(root, "created")); !os.IsNotExist(err) {
		t.Fatalf("directory was created without the right: %v", err)
	}
	e.fs.fds[3].rights |= rightPathCreateDirectory
	copy(m.mem[32:], "allowed")
	e.pathOpen(m, []uint64{3, 0, 32, 7, 3, rightFDRead, 0, 0, 16}, r)
	if r[0] != wasiOK {
		t.Fatalf("path_open CREAT|DIRECTORY with directory right: errno %d", r[0])
	}
	fd := uint64(binary.LittleEndian.Uint32(m.mem[16:]))
	e.fdClose(m, []uint64{fd}, r)
	if info, err := os.Stat(filepath.Join(root, "allowed")); err != nil || !info.IsDir() {
		t.Fatalf("authorized directory = %v, %v", info, err)
	}
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

func TestWindowsSetPathTimesMixedTerminalSeparators(t *testing.T) {
	for _, tc := range []struct {
		name string
		want uint64
	}{
		{`sub/\`, wasiOK},
		{`sub\/`, wasiOK},
		{`file/\`, wasiENotdir},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "sub"), 0o700); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(root, "file")
			if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
				t.Fatal(err)
			}
			fileBefore, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true, MutateDirectory: true}}})
			defer e.closeAll()
			want := time.Date(2001, time.February, 3, 4, 5, 6, 0, time.UTC)
			mem := []byte(tc.name)
			result := []uint64{999}
			e.pathFilestatSetTimes(testModule{mem}, []uint64{3, 0, 0, uint64(len(tc.name)), 0, uint64(want.UnixNano()), 4}, result)
			if result[0] != tc.want {
				t.Fatalf("set times %q: errno %d, want %d", tc.name, result[0], tc.want)
			}
			if tc.want == wasiOK {
				info, err := os.Stat(filepath.Join(root, "sub"))
				if err != nil || !info.ModTime().Equal(want) {
					t.Fatalf("directory mtime = %v, %v; want %v", info, err, want)
				}
			} else {
				info, err := os.Stat(file)
				if err != nil || !info.ModTime().Equal(fileBefore.ModTime()) {
					t.Fatalf("file mtime changed = %v, %v", info, err)
				}
			}
		})
	}
}

func TestWindowsGetPathStatMixedTerminalSeparators(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true, MutateDirectory: true}}})
	defer e.closeAll()
	for _, tc := range []struct {
		name string
		want uint64
	}{
		{`sub/\`, wasiOK},
		{`file/\`, wasiENotdir},
	} {
		mem := make([]byte, 256)
		copy(mem, tc.name)
		result := []uint64{999}
		e.pathFilestatGet(testModule{mem}, []uint64{3, 0, 0, uint64(len(tc.name)), 64}, result)
		if result[0] != tc.want {
			t.Errorf("stat %q: errno %d, want %d", tc.name, result[0], tc.want)
		}
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

func TestWindowsPathOpenTrailingSlashRequiresDirectoryCreationRight(t *testing.T) {
	root := t.TempDir()
	e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true, MutateDirectory: true}}})
	defer closeFS(e.fs)
	e.fs.fds[3].rights &^= rightPathCreateDirectory
	m := testModule{mem: make([]byte, 128)}
	copy(m.mem[32:], "created/")
	r := make([]uint64, 1)
	e.pathOpen(m, []uint64{3, 0, 32, 8, 1, rightFDRead, 0, 0, 16}, r)
	if r[0] != wasiENotcapable {
		t.Fatalf("CREAT with trailing slash: %d, want ENOTCAPABLE", r[0])
	}
	if _, err := os.Stat(filepath.Join(root, "created")); !os.IsNotExist(err) {
		t.Fatalf("unauthorized directory: %v", err)
	}
	e.fs.fds[3].rights |= rightPathCreateDirectory
	copy(m.mem[32:], "allowed/")
	e.pathOpen(m, []uint64{3, 0, 32, 8, 1, rightFDRead, 0, 0, 16}, r)
	if r[0] != wasiOK {
		t.Fatalf("authorized CREAT with trailing slash: %d", r[0])
	}
	fd := uint64(binary.LittleEndian.Uint32(m.mem[16:]))
	e.fdClose(m, []uint64{fd}, r)
	if info, err := os.Stat(filepath.Join(root, "allowed")); err != nil || !info.IsDir() {
		t.Fatalf("authorized directory=%v, %v", info, err)
	}
}

func TestWindowsSizeRightsSurviveAppendFlags(t *testing.T) {
	for _, right := range []uint64{rightFDFilestatSetSize, rightFDAllocate} {
		for _, initialAppend := range []uint64{0, 1} {
			t.Run(fmt.Sprintf("right%d/append%d", right, initialAppend), func(t *testing.T) {
				root := t.TempDir()
				if err := os.WriteFile(filepath.Join(root, "file"), []byte("payload"), 0o600); err != nil {
					t.Fatal(err)
				}
				e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true}}})
				defer closeFS(e.fs)
				m := testModule{mem: make([]byte, 128)}
				copy(m.mem[32:], "file")
				r := make([]uint64, 1)
				e.pathOpen(m, []uint64{3, 0, 32, 4, 0, right | rightFDStatSetFlags, 0, initialAppend, 16}, r)
				if r[0] != wasiOK {
					t.Fatalf("path_open: %d", r[0])
				}
				fd := uint64(binary.LittleEndian.Uint32(m.mem[16:]))
				for index, flags := range []uint64{initialAppend, 1 - initialAppend, initialAppend} {
					e.fdFdstatSetFlags(m, []uint64{fd, flags}, r)
					if r[0] != wasiOK {
						t.Fatalf("set flags %d: %d", flags, r[0])
					}
					if right == rightFDFilestatSetSize {
						e.fdFilestatSetSize(m, []uint64{fd, 2}, r)
					} else {
						e.fdAllocate(m, []uint64{fd, 0, uint64(16 + index)}, r)
					}
					if r[0] != wasiOK {
						t.Fatalf("size operation with flags %d: %d", flags, r[0])
					}
					info, err := os.Stat(filepath.Join(root, "file"))
					want := int64(2)
					if right == rightFDAllocate {
						want = int64(16 + index)
					}
					if err != nil || info.Size() != want {
						t.Fatalf("size=%v, %v; want %d", info, err, want)
					}
					e.fdWrite(m, []uint64{fd, 64, 0, 80}, r)
					if r[0] != wasiENotcapable {
						t.Fatalf("ungranted write right: %d", r[0])
					}
				}
			})
		}
	}
}

func TestWindowsSizeChangeRetainsAtomicAppendHandle(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true}}})
	defer closeFS(e.fs)
	m := testModule{mem: make([]byte, 128)}
	copy(m.mem[32:], "file")
	r := make([]uint64, 1)
	e.pathOpen(m, []uint64{3, 0, 32, 4, 0, rightFDWrite | rightFDFilestatSetSize | rightFDStatSetFlags | rightFDSeek, 0, 1, 16}, r)
	if r[0] != wasiOK {
		t.Fatalf("path_open: %d", r[0])
	}
	fd := uint64(binary.LittleEndian.Uint32(m.mem[16:]))
	original := e.fs.fds[uint32(fd)].file
	e.fdFilestatSetSize(m, []uint64{fd, 2}, r)
	if r[0] != wasiOK {
		t.Fatalf("set_size with APPEND: %d", r[0])
	}
	if e.fs.fds[uint32(fd)].file != original {
		t.Fatal("size operation replaced append handle")
	}
	if _, err := original.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(m.mem[64:], 96)
	binary.LittleEndian.PutUint32(m.mem[68:], 1)
	m.mem[96] = 'x'
	e.fdWrite(m, []uint64{fd, 64, 1, 80}, r)
	if r[0] != wasiOK {
		t.Fatalf("append write after size change: %d", r[0])
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "pax" {
		t.Fatalf("append data=%q, %v; want pax", data, err)
	}
}

func TestWindowsPathOpenAppendTruncWithoutWriteRight(t *testing.T) {
	for _, rights := range []uint64{rightFDRead, rightFDFilestatSetSize} {
		t.Run(fmt.Sprintf("rights%d", rights), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "file")
			if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
				t.Fatal(err)
			}
			e := newTestPlugin(t, Config{Mounts: []Preopen{{GuestPath: "/data", HostPath: root, Read: true, Write: true}}})
			defer closeFS(e.fs)
			m := testModule{mem: make([]byte, 128)}
			copy(m.mem[32:], "file")
			r := make([]uint64, 1)
			e.pathOpen(m, []uint64{3, 0, 32, 4, 8, rights, 0, 1, 16}, r)
			if r[0] != wasiOK {
				t.Fatalf("APPEND|TRUNC without FD_WRITE: %d", r[0])
			}
			info, err := os.Stat(path)
			if err != nil || info.Size() != 0 {
				t.Fatalf("size=%v, %v; want 0", info, err)
			}
		})
	}
}
