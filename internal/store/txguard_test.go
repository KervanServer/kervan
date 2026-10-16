package store

import (
	"errors"
	"testing"
	"time"
)

// runWithin fails the test instead of hanging if fn deadlocks.
func runWithin(t *testing.T, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlocked")
	}
}

func TestStoreMethodsInsideUpdateReturnErrInsideUpdate(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Put("c", "k", "v"); err != nil {
		t.Fatalf("put: %v", err)
	}
	calls := map[string]func() error{
		"Get":    func() error { var v string; return st.Get("c", "k", &v) },
		"List":   func() error { var v []string; return st.List("c", &v) },
		"Put":    func() error { return st.Put("c", "k2", "v") },
		"Delete": func() error { return st.Delete("c", "k") },
		"Update": func() error { return st.Update(func(*Tx) error { return nil }) },
		"Close":  func() error { return st.Close() },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			runWithin(t, func() {
				var inner error
				if err := st.Update(func(*Tx) error {
					inner = call()
					return nil
				}); err != nil {
					t.Errorf("update: %v", err)
				}
				if !errors.Is(inner, ErrInsideUpdate) {
					t.Errorf("%s inside Update: got %v, want ErrInsideUpdate", name, inner)
				}
			})
		})
	}
	// The rejected calls had no effect, and the store works again afterwards.
	var v string
	if err := st.Get("c", "k", &v); err != nil || v != "v" {
		t.Fatalf("get after: %q, %v", v, err)
	}
	if err := st.Get("c", "k2", &v); !errors.Is(err, ErrNotFound) {
		t.Fatalf("k2 should not exist, got %v", err)
	}
}

func TestStoreReadsFromOtherGoroutinesDuringUpdate(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Put("c", "k", "v"); err != nil {
		t.Fatalf("put: %v", err)
	}
	runWithin(t, func() {
		_ = st.Update(func(tx *Tx) error {
			result := make(chan error, 1)
			go func() {
				var v string
				result <- st.Get("c", "k", &v)
			}()
			if err := <-result; err != nil {
				t.Errorf("concurrent Get during Update: %v", err)
			}
			return nil
		})
	})
}

func TestStoreUpdateClearsOwnerAfterPanic(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	func() {
		defer func() { _ = recover() }()
		_ = st.Update(func(*Tx) error { panic("boom") })
	}()
	if err := st.Put("c", "k", "v"); err != nil {
		t.Fatalf("put after panicking Update: %v", err)
	}
}

func TestGoroutineIDDistinguishesGoroutines(t *testing.T) {
	here := goroutineID()
	if here == 0 {
		t.Fatal("goroutineID returned 0")
	}
	other := make(chan uint64)
	go func() { other <- goroutineID() }()
	if id := <-other; id == 0 || id == here {
		t.Fatalf("other goroutine id %d, this one %d", id, here)
	}
}
