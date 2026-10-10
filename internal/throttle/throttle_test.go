package throttle

import (
	"testing"
	"time"
)

// fakeClock advances only when the limiter sleeps.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time        { return c.t }
func (c *fakeClock) sleep(d time.Duration) { c.t = c.t.Add(d) }
func newFake(rate int64) (*Limiter, *fakeClock) {
	c := &fakeClock{t: time.Unix(0, 0)}
	l := NewLimiter(rate)
	l.now, l.sleep, l.last = c.now, c.sleep, c.t
	return l, c
}

func TestLimiterEnforcesRate(t *testing.T) {
	const rate = 1 << 20 // 1 MiB/s
	l, clock := newFake(rate)
	start := clock.t
	chunk := l.ChunkSize()
	for sent := 0; sent < 10<<20; sent += chunk {
		l.Wait(chunk)
	}
	elapsed := clock.t.Sub(start)
	// 10 MiB at 1 MiB/s, minus the initial burst.
	if elapsed < 9*time.Second || elapsed > 10*time.Second {
		t.Fatalf("10 MiB took %v at 1 MiB/s", elapsed)
	}
}

func TestLimiterLargeRequestPaysDebt(t *testing.T) {
	l, clock := newFake(100 << 10)
	start := clock.t
	l.Wait(1 << 20) // far larger than the burst
	if d := clock.t.Sub(start); d < 9*time.Second {
		t.Fatalf("1 MiB at 100 KiB/s waited only %v", d)
	}
}

func TestLimiterNilAndUnlimited(t *testing.T) {
	var l *Limiter
	l.Wait(1 << 30)
	l.SetRate(5)
	if NewLimiter(0) != nil || l.ChunkSize() != 0 {
		t.Fatal("zero rate must be unlimited")
	}
	lim, clock := newFake(1 << 10)
	lim.SetRate(0)
	before := clock.t
	lim.Wait(1 << 30)
	if clock.t != before {
		t.Fatal("rate 0 after SetRate must not wait")
	}
}

func TestRegistrySharesPerUser(t *testing.T) {
	r := NewRegistry()
	a := r.ForUser("ann", 1000)
	if a == nil || r.ForUser("ann", 2000) != a {
		t.Fatal("same user must share one limiter")
	}
	if a.rate != 2000 {
		t.Fatal("rate not updated")
	}
	if r.ForUser("bob", 1000) == a {
		t.Fatal("users must not share limiters")
	}
	if r.ForUser("ann", 0) != nil || a.rate != 0 {
		t.Fatal("unlimited must disable the shared limiter")
	}
}
