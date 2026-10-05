package p2

import (
	"sync"
	"testing"
)

func TestNetworkHandlesBoundedAndReclaimed(t *testing.T) {
	s := &hostState{limits: Limits{MaxNetworkHandles: 2}.normalized()}
	for i := 0; i < 2; i++ {
		if _, err := s.acquireNetwork(); err != nil {
			t.Fatalf("network %d: %v", i, err)
		}
	}
	if _, err := s.acquireNetwork(); err == nil {
		t.Fatal("third network handle exceeded quota")
	}
	s.releaseNetwork(1)
	if _, err := s.acquireNetwork(); err != nil {
		t.Fatalf("network after drop: %v", err)
	}
}

func TestNetworkHandleLimitConcurrent(t *testing.T) {
	s := &hostState{limits: Limits{MaxNetworkHandles: 1}.normalized()}
	const callers = 32
	var wg sync.WaitGroup
	results := make(chan bool, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.acquireNetwork()
			results <- err == nil
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for ok := range results {
		if ok {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent network acquisitions = %d successes, want 1", successes)
	}
}
