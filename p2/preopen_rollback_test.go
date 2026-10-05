package p2

import (
	"fmt"
	"testing"
)

type testDescriptorHandles struct {
	next uint32
	live map[uint32]uint32
}

func (t *testDescriptorHandles) NewOwn(_ uint32, rep uint32) uint32 {
	t.next++
	if t.live == nil {
		t.live = make(map[uint32]uint32)
	}
	t.live[t.next] = rep
	return t.next
}

func (t *testDescriptorHandles) TakeOwn(_ uint32, handle uint32) (uint32, error) {
	rep, ok := t.live[handle]
	if !ok {
		return 0, fmt.Errorf("unknown descriptor handle %d", handle)
	}
	delete(t.live, handle)
	return rep, nil
}

func TestGetDirectoriesRollsBackEarlierDescriptors(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name   string
		second string
		limit  uint32
	}{
		{"later mount fails", root + "/missing", 2},
		{"descriptor quota fails", root, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newFilesystem([]Preopen{
				{GuestPath: "/a", HostPath: root, Read: true},
				{GuestPath: "/z", HostPath: tc.second, Read: true},
			}, Limits{MaxDescriptors: tc.limit})
			defer func() {
				for _, node := range s.descs {
					node.file.Close()
				}
			}()
			handles := &testDescriptorHandles{}
			if _, err := s.getDirectories(handles); err == nil {
				t.Fatal("get-directories succeeded despite a later failure")
			}
			if len(s.descs) != 0 || len(handles.live) != 0 {
				t.Fatalf("after failed get-directories: %d descriptors, %d handles remain", len(s.descs), len(handles.live))
			}
		})
	}
}
