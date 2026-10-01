package core

import (
	"fmt"
	"runtime"
	"testing"
)

func TestCloseFSDropsDescriptorMap(t *testing.T) {
	s := &fsState{fds: make(map[uint32]*fdEntry)}
	for i := uint32(0); i < 4096; i++ {
		s.fds[i] = &fdEntry{}
	}
	closeFS(s)
	if s.fds != nil {
		t.Fatal("terminal state retains descriptor map storage")
	}
	closeFS(s)
}

var closedFSState *fsState

func BenchmarkFSTerminalLifecycle(b *testing.B) {
	for _, count := range []int{3, 1024} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				s := &fsState{fds: make(map[uint32]*fdEntry, count)}
				for fd := 0; fd < count; fd++ {
					s.fds[uint32(fd)] = &fdEntry{}
				}
				closeFS(s)
				closedFSState = s
			}
		})
	}
}

var retainedFSStates []*fsState

// This is a fixed-work post-GC diagnostic, not a production memory endpoint.
func BenchmarkFSTerminalRetainedHeap(b *testing.B) {
	if b.N != 1 {
		b.Fatal("use -benchtime=1x for this fixed-work diagnostic")
	}
	runtime.GC()
	retainedFSStates = make([]*fsState, 8)
	for i := range retainedFSStates {
		s := &fsState{fds: make(map[uint32]*fdEntry, 4096)}
		entry := &fdEntry{}
		for fd := uint32(0); fd < 4096; fd++ {
			s.fds[fd] = entry
		}
		closeFS(s)
		retainedFSStates[i] = s
	}
	var normal, after runtime.MemStats
	runtime.ReadMemStats(&normal)
	runtime.GC()
	runtime.ReadMemStats(&after)
	b.ReportMetric(float64(normal.HeapAlloc), "normal-heap-B")
	b.ReportMetric(float64(after.HeapAlloc), "postgc-heap-B")
	runtime.KeepAlive(retainedFSStates)
}
