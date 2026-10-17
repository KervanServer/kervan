package store

import (
	"errors"
	"runtime"
)

// ErrInsideUpdate is returned by Store methods called from the goroutine
// running an Update callback. Those calls would either deadlock on persistMu
// (Put, Delete, Close, Update) or read pre-transaction state and defeat the
// callback's checks (Get, List); the callback must use its Tx instead.
var ErrInsideUpdate = errors.New("store: Store method called inside an Update callback; use the Tx")

// checkNotInUpdate reports ErrInsideUpdate when the calling goroutine is the
// one running an Update callback. Outside a transaction it costs one atomic
// load; other goroutines reading during a transaction are unaffected. A
// goroutine spawned by the callback is not detected.
func (s *Store) checkNotInUpdate() error {
	owner := s.txOwner.Load()
	if owner != 0 && owner == goroutineID() {
		return ErrInsideUpdate
	}
	return nil
}

// runTx runs fn with the calling goroutine marked as the transaction owner,
// clearing the mark even if fn panics. Backends run at most one transaction
// per Store at a time, so there is at most one owner.
func (s *Store) runTx(fn func(tx *Tx) error, tx *Tx) error {
	s.txOwner.Store(goroutineID())
	defer s.txOwner.Store(0)
	return fn(tx)
}

// goroutineID parses the current goroutine's id from its stack header,
// "goroutine <id> [...". Go exposes no API for it; it is only used to tell
// the transaction's own goroutine apart from concurrent callers.
func goroutineID() uint64 {
	var buf [64]byte
	b := buf[:runtime.Stack(buf[:], false)]
	const prefix = "goroutine "
	if len(b) <= len(prefix) {
		return 0
	}
	var id uint64
	for _, c := range b[len(prefix):] {
		if c < '0' || c > '9' {
			break
		}
		id = id*10 + uint64(c-'0')
	}
	return id
}
