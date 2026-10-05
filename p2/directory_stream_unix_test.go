//go:build linux || darwin

package p2

import (
	"os"
	"testing"
)

func TestDirectoryStreamsStartIndependently(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(root+"/"+name, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	first, err := newDirectoryStreamFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := newDirectoryStreamFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	firstEntry, err := first.ReadDir(1)
	if err != nil || len(firstEntry) != 1 {
		t.Fatalf("first stream = %v, %v", firstEntry, err)
	}
	secondEntry, err := second.ReadDir(1)
	if err != nil || len(secondEntry) != 1 || secondEntry[0].Name() != firstEntry[0].Name() {
		t.Fatalf("second stream first entry = %v, %v; want %q", secondEntry, err, firstEntry[0].Name())
	}
}
