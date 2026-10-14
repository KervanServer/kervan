package auth

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHashBudgetBoundsConcurrency(t *testing.T) {
	b := newHashBudget(100)
	var inFlight, peak atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !b.acquire(40, 5*time.Second) {
				t.Error("acquire timed out")
				return
			}
			n := inFlight.Add(40)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			inFlight.Add(-40)
			b.release(40)
		}()
	}
	wg.Wait()
	if peak.Load() > 100 {
		t.Fatalf("budget exceeded: peak %d of 100", peak.Load())
	}
}

func TestHashBudgetOversizedRunsAloneAndTimesOut(t *testing.T) {
	b := newHashBudget(100)
	if !b.acquire(1000, time.Second) { // capped at the whole budget
		t.Fatal("oversized acquire on an idle budget failed")
	}
	start := time.Now()
	if b.acquire(1, 50*time.Millisecond) {
		t.Fatal("acquire succeeded while the budget was full")
	}
	if time.Since(start) > time.Second {
		t.Fatal("timeout not honoured")
	}
	b.release(1000)
	if !b.acquire(100, time.Second) {
		t.Fatal("budget not returned on release")
	}
}
