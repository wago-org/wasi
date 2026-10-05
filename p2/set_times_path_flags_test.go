package p2

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	component "github.com/wago-org/component-model"
)

func timestampForTest(want time.Time) component.Value {
	return component.VariantValue{Disc: 2, Payload: []component.Value{uint64(want.Unix()), uint32(want.Nanosecond())}}
}

func TestSetTimesUnderPathFlagsUsesSymlinkFlag(t *testing.T) {
	for _, flags := range []uint32{0, 1} {
		name := "link itself"
		if flags == 1 {
			name = "link target"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, "file")
			link := filepath.Join(root, "link")
			if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
				t.Fatal(err)
			}
			base, err := openPreopenDirectory(root, true)
			if err != nil {
				t.Fatal(err)
			}
			defer base.Close()
			if err := hostFS.Symlinkat("file", int(base.Fd()), "link"); err != nil {
				t.Fatal(err)
			}
			beforeFile, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			beforeLink, err := os.Lstat(link)
			if err != nil {
				t.Fatal(err)
			}
			want := time.Date(2005, time.June, 7, 8, 9, 10, 0, time.UTC)
			if err := setTimesUnderPathFlags(base, "link", flags, component.VariantValue{Disc: 0}, timestampForTest(want), time.Now); err != nil {
				t.Fatal(err)
			}
			afterFile, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			afterLink, err := os.Lstat(link)
			if err != nil {
				t.Fatal(err)
			}
			wantFile, wantLink := beforeFile.ModTime(), want
			if flags == 1 {
				wantFile, wantLink = want, beforeLink.ModTime()
			}
			if !afterFile.ModTime().Equal(wantFile) || !afterLink.ModTime().Equal(wantLink) {
				t.Fatalf("file/link mtimes = %v/%v, want %v/%v", afterFile.ModTime(), afterLink.ModTime(), wantFile, wantLink)
			}
		})
	}
}

func TestSetTimesUnderPathFlagsOrdinaryFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := openPreopenDirectory(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	want := time.Date(2006, time.July, 8, 9, 10, 11, 0, time.UTC)
	for _, flags := range []uint32{0, 1} {
		if err := setTimesUnderPathFlags(base, "file", flags, timestampForTest(want), timestampForTest(want), time.Now); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(root, "file"))
		if err != nil || !info.ModTime().Equal(want) {
			t.Fatalf("ordinary file mtime = %v, %v", info, err)
		}
	}
}

func TestSetTimesUnderPathFlagsRejectsUnknownFlags(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := openPreopenDirectory(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	want := time.Date(2006, time.July, 8, 9, 10, 11, 0, time.UTC)
	if err := setTimesUnderPathFlags(base, "file", 2, timestampForTest(want), timestampForTest(want), time.Now); err == nil || fsError(err) != fsErrInvalid {
		t.Fatalf("unknown path flags = %v, want invalid", err)
	}
}
