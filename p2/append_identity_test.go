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
	base, err := openPreopenDirectory(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	var want *appendTarget
	for _, name := range []string{"original", "original", "alias"} {
		f, err := openUnder(base, name, hostFS.O_RDWR, 0)
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
	if err := os.Rename(name, filepath.Join(root, "renamed")); err != nil {
		t.Fatal(err)
	}
	f, err := openUnder(base, "renamed", hostFS.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	node := &descriptorNode{file: f}
	if _, err := newFilesystem(nil, Limits{}).addDesc(node); err != nil {
		t.Fatal(err)
	}
	if node.append != want {
		t.Fatal("rename must preserve the append lock for live file aliases")
	}
}
