// Package throttle limits transfer bandwidth with token buckets: one per
// user (shared by all of that user's connections) plus an optional global
// bucket for the whole server.
package throttle

import (
	"sync"
	"time"
)

// Limiter is a token bucket in bytes per second. Callers take tokens before
// moving data and may go into debt, sleeping until it is repaid, so a large
// request is delayed rather than starved. A nil *Limiter is unlimited.
type Limiter struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time

	now   func() time.Time
	sleep func(time.Duration)
}

// NewLimiter returns a limiter for rate bytes/s; rate <= 0 returns nil
// (unlimited).
func NewLimiter(rate int64) *Limiter {
	if rate <= 0 {
		return nil
	}
	l := &Limiter{now: time.Now, sleep: time.Sleep}
	l.setRateLocked(rate)
	l.tokens = l.burst
	l.last = l.now()
	return l
}

func (l *Limiter) setRateLocked(rate int64) {
	l.rate = float64(rate)
	// Allow a quarter second of traffic (at least one 32 KiB chunk) as
	// burst so small reads are not paced individually.
	l.burst = max(l.rate/4, 32<<10)
	l.tokens = min(l.tokens, l.burst)
}

// NewAdjustable returns a limiter that always exists, so its rate can later
// be raised from unlimited (0) without replacing it.
func NewAdjustable(rate int64) *Limiter {
	l := &Limiter{now: time.Now, sleep: time.Sleep}
	l.last = l.now()
	if rate > 0 {
		l.setRateLocked(rate)
		l.tokens = l.burst
	}
	return l
}

// SetRate changes the limit; rate <= 0 means unlimited.
func (l *Limiter) SetRate(rate int64) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if rate <= 0 {
		l.rate = 0
		return
	}
	l.setRateLocked(rate)
}

// ChunkSize is the largest piece worth requesting at once, so a single
// transfer call never sleeps for more than about a quarter second.
func (l *Limiter) ChunkSize() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rate <= 0 {
		return 0
	}
	return int(max(l.burst, 4<<10))
}

// Wait blocks until n bytes may pass.
func (l *Limiter) Wait(n int) {
	if l == nil || n <= 0 {
		return
	}
	l.mu.Lock()
	if l.rate <= 0 {
		l.mu.Unlock()
		return
	}
	now := l.now()
	l.tokens = min(l.burst, l.tokens+now.Sub(l.last).Seconds()*l.rate)
	l.last = now
	l.tokens -= float64(n)
	var delay time.Duration
	if l.tokens < 0 {
		delay = time.Duration(-l.tokens / l.rate * float64(time.Second))
	}
	l.mu.Unlock()
	if delay > 0 {
		l.sleep(delay)
	}
}

// Registry hands out one shared limiter per user, so parallel connections
// share the user's limit.
type Registry struct {
	mu    sync.Mutex
	users map[string]*Limiter
}

func NewRegistry() *Registry {
	return &Registry{users: make(map[string]*Limiter)}
}

// ForUser returns username's limiter at rate bytes/s (nil when unlimited),
// updating the rate of an existing limiter.
func (r *Registry) ForUser(username string, rate int64) *Limiter {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	l := r.users[username]
	if rate <= 0 {
		if l != nil {
			l.SetRate(0)
		}
		return nil
	}
	if l == nil {
		l = NewLimiter(rate)
		r.users[username] = l
		return l
	}
	l.SetRate(rate)
	return l
}
