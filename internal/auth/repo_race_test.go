// Regression tests for read-modify-write races on user records: each check
// and its writes must happen in one store transaction, and failed logins must
// be counted against the stored record rather than a stale copy.
package auth_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/store"
)

func openStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func countUsername(t *testing.T, repo *auth.UserRepository, name string) int {
	t.Helper()
	users, err := repo.List()
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	n := 0
	for _, u := range users {
		if u.Username == name {
			n++
		}
	}
	return n
}

// Concurrent creators of one username must not all pass the uniqueness
// check: Create checks and writes the user and index in one transaction.
func TestUserRepositoryConcurrentCreateSameName(t *testing.T) {
	repo := auth.NewUserRepository(openStore(t, t.TempDir()))
	const workers = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = repo.Create(&auth.User{Username: "alice", Enabled: true})
		}()
	}
	close(start)
	wg.Wait()
	if n := countUsername(t, repo, "alice"); n != 1 {
		t.Fatalf("expected exactly 1 user named alice, found %d", n)
	}
}

// The same holds across two Store handles on one data dir, as when the server
// and a CLI "user create" open the store file concurrently.
func TestUserRepositoryConcurrentCreateAcrossStores(t *testing.T) {
	dir := t.TempDir()
	server := auth.NewUserRepository(openStore(t, dir))
	cli := auth.NewUserRepository(openStore(t, dir))
	const perSide = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < perSide; i++ {
		for _, repo := range []*auth.UserRepository{server, cli} {
			wg.Add(1)
			go func(r *auth.UserRepository) {
				defer wg.Done()
				<-start
				_ = r.Create(&auth.User{Username: "bob", Enabled: true})
			}(repo)
		}
	}
	close(start)
	wg.Wait()
	if n := countUsername(t, server, "bob"); n != 1 {
		t.Fatalf("expected exactly 1 user named bob, found %d", n)
	}
}

// UpdateLastLogin must only touch login fields of the stored record, so an
// admin disable racing a login is never reverted.
func TestUserRepositoryUpdateLastLoginKeepsConcurrentDisable(t *testing.T) {
	repo := auth.NewUserRepository(openStore(t, t.TempDir()))
	const trials = 60
	reverted := 0
	for i := 0; i < trials; i++ {
		u := &auth.User{Username: fmt.Sprintf("carol%d", i), Enabled: true}
		if err := repo.Create(u); err != nil {
			t.Fatalf("create: %v", err)
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() { // login path
			defer wg.Done()
			<-start
			_ = repo.UpdateLastLogin(u.ID)
		}()
		go func() { // admin disables the account
			defer wg.Done()
			<-start
			cur, err := repo.GetByID(u.ID)
			if err != nil || cur == nil {
				return
			}
			cur.Enabled = false
			_ = repo.Update(cur)
		}()
		close(start)
		wg.Wait()
		final, err := repo.GetByID(u.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if final.Enabled {
			reverted++
		}
	}
	if reverted > 0 {
		t.Fatalf("admin disable was reverted by a concurrent UpdateLastLogin in %d/%d trials", reverted, trials)
	}
}

// Every parallel wrong-password attempt must be counted. authenticate reads
// the user before the slow password check, so incrementing that stale copy
// undercounted (20 attempts recorded as 1).
func TestEngineConcurrentFailedLoginsAllCounted(t *testing.T) {
	repo := auth.NewUserRepository(openStore(t, t.TempDir()))
	engine := auth.NewEngine(repo, "argon2id", 1000, 0)
	hash, err := auth.HashPassword("correct-horse-battery", "argon2id")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u := &auth.User{Username: "dave", Enabled: true, PasswordHash: hash}
	if err := repo.Create(u); err != nil {
		t.Fatalf("create: %v", err)
	}
	const attempts = 20
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _ = engine.Authenticate(context.Background(), "dave", "wrong")
		}()
	}
	close(start)
	wg.Wait()
	final, err := repo.GetByID(u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if final.FailedLogins != attempts {
		t.Fatalf("expected FailedLogins=%d after %d failed attempts, got %d", attempts, attempts, final.FailedLogins)
	}
}

// Parallel attempts must not get more than max_attempts password checks
// before the account locks (security.brute_force.max_attempts).
func TestEngineParallelGuessesRespectLockout(t *testing.T) {
	repo := auth.NewUserRepository(openStore(t, t.TempDir()))
	const maxAttempts = 5
	engine := auth.NewEngine(repo, "argon2id", maxAttempts, 0)
	hash, err := auth.HashPassword("correct-horse-battery", "argon2id")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u := &auth.User{Username: "erin", Enabled: true, PasswordHash: hash}
	if err := repo.Create(u); err != nil {
		t.Fatalf("create: %v", err)
	}
	const attempts = 20
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		guessed int // attempts that reached the password check
	)
	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := engine.Authenticate(context.Background(), "erin", "wrong")
			if errors.Is(err, auth.ErrInvalidCredentials) {
				mu.Lock()
				guessed++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	if guessed > maxAttempts {
		t.Fatalf("max_attempts=%d but %d parallel guesses were checked against the password", maxAttempts, guessed)
	}
}
