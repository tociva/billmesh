package app

import (
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestSecuritySSELimitIsPerAccountAndReleased(t *testing.T) {
	a := NewAPI(nil, nil, nil)
	first, second := uuid.New(), uuid.New()
	var wg sync.WaitGroup
	allowed := make(chan bool, maxSSEConnectionsPerAccount*2)
	for range maxSSEConnectionsPerAccount * 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed <- a.acquireSSE(first)
		}()
	}
	wg.Wait()
	close(allowed)
	count := 0
	for ok := range allowed {
		if ok {
			count++
		}
	}
	if count != maxSSEConnectionsPerAccount {
		t.Fatalf("want %d admitted streams, got %d", maxSSEConnectionsPerAccount, count)
	}
	if !a.acquireSSE(second) {
		t.Fatal("one account exhausted another account's stream capacity")
	}
	a.releaseSSE(first)
	if !a.acquireSSE(first) {
		t.Fatal("a closed stream did not return capacity")
	}
	for range maxSSEConnectionsPerAccount {
		a.releaseSSE(first)
	}
	a.releaseSSE(second)
	if len(a.sseActive) != 0 {
		t.Fatalf("closed streams still tracked: %v", a.sseActive)
	}
}
