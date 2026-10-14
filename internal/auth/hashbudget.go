package auth

import (
	"sync"
	"time"
)

// Argon2id allocates its whole memory cost on every hash and verification
// (64 MiB by default, up to 1 GiB for imported hashes). Without a bound, a
// burst of concurrent sign-in attempts, even with wrong passwords, makes
// the server allocate memory per attempt before any lockout or IP ban can
// react.
// hashBudget caps the memory all in-flight Argon2 computations may hold.
type hashBudget struct {
	mu    sync.Mutex
	cond  *sync.Cond
	limit uint64 // KiB
	used  uint64 // KiB
}

// defaultHashBudgetKiB (256 MiB) allows four default-cost (64 MiB)
// computations at once, about 50 password checks per second. A hash
// declaring more than the whole budget (up to 1 GiB for imported hashes)
// runs alone. Measured: 300 simultaneous wrong-password logins peak at
// ~540 MB RSS (13.7 GB without the budget), ~410 MB with GOMEMLIMIT=400MiB.
const defaultHashBudgetKiB = 256 << 10

// hashWaitTimeout bounds how long a sign-in waits for budget; under a flood
// it fails instead of queueing without limit.
const hashWaitTimeout = 30 * time.Second

var argon2Budget = newHashBudget(defaultHashBudgetKiB)

func newHashBudget(limitKiB uint64) *hashBudget {
	b := &hashBudget{limit: limitKiB}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// acquire reserves cost KiB (capped at the whole budget, so one oversized
// computation can still run alone). It returns false if the budget did not
// free up within timeout.
func (b *hashBudget) acquire(cost uint64, timeout time.Duration) bool {
	cost = min(cost, b.limit)
	deadline := time.Now().Add(timeout)
	// sync.Cond has no timed wait; a timer broadcast wakes waiters so they
	// can notice the deadline.
	timer := time.AfterFunc(timeout, func() {
		b.mu.Lock()
		b.cond.Broadcast()
		b.mu.Unlock()
	})
	defer timer.Stop()
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.used+cost > b.limit {
		if !time.Now().Before(deadline) {
			return false
		}
		b.cond.Wait()
	}
	b.used += cost
	return true
}

func (b *hashBudget) release(cost uint64) {
	cost = min(cost, b.limit)
	b.mu.Lock()
	b.used -= cost
	b.mu.Unlock()
	b.cond.Broadcast()
}
