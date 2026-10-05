package p2

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendTargetIsSharedByFileAliases(t *testing.T) {
	root := t.TempDir()
	name := filepath.Join(root, "original")
	if err := os.WriteFile(name, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Link(name, alias); err != nil {
		t.Fatal(err)
	}
	var want *appendTarget
	for _, path := range []string{name, name, alias} {
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		// Distinct instances must still use the same append lock.
		s := newFilesystem(nil, Limits{})
		node := &descriptorNode{file: f}
		if _, err := s.addDesc(node); err != nil {
			t.Fatal(err)
		}
		if want == nil {
			want = node.append
		} else if node.append != want {
			t.Fatal("independent handles and hard-link aliases must share one append lock")
		}
	}
}
