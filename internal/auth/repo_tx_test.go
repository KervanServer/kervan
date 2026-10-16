package auth

import (
	"fmt"
	"sync"
	"testing"

	"github.com/kervanserver/kervan/internal/store"
)

func newTxTestEngine(t *testing.T) (*Engine, *UserRepository) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	repo := NewUserRepository(st)
	return NewEngine(repo, "bcrypt", 5, 0), repo
}

// syncLDAPUser receives the shadow record read before the LDAP bind. A local
// edit saved during the bind (here: an admin disable and new SSH keys) must
// survive the sync, and the login must then be refused.
func TestSyncLDAPUserKeepsLocalEditsMadeDuringBind(t *testing.T) {
	e, repo := newTxTestEngine(t)
	shadow := &User{Username: "lena", AuthProvider: AuthProviderLDAP, Enabled: true}
	if err := repo.Create(shadow); err != nil {
		t.Fatalf("create: %v", err)
	}
	stale, err := repo.GetByID(shadow.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := repo.Mutate(shadow.ID, func(u *User) error {
		u.Enabled = false
		u.AuthorizedKeys = []string{"ssh-ed25519 AAAA test"}
		return nil
	}); err != nil {
		t.Fatalf("admin edit: %v", err)
	}
	identity := &LDAPIdentity{Username: "lena", Email: "lena@example.test", Groups: []string{"eng", "ops"}, Type: UserTypeVirtual}
	synced, err := e.syncLDAPUser(identity, stale)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if synced.Enabled || len(synced.AuthorizedKeys) != 1 {
		t.Fatalf("sync reverted local edits: enabled=%v keys=%v", synced.Enabled, synced.AuthorizedKeys)
	}
	if synced.Email != "lena@example.test" || synced.PrimaryGroup != "eng" || len(synced.SecondaryGrps) != 1 {
		t.Fatalf("directory fields not applied: %+v", synced)
	}
	if err := e.ensureLoginAllowed(synced); err != ErrUserDisabled {
		t.Fatalf("disabled account must be refused after sync, got %v", err)
	}
}

// ResetPassword must change only credential fields: an admin disable racing
// a password reset is never reverted.
func TestResetPasswordKeepsConcurrentDisable(t *testing.T) {
	e, repo := newTxTestEngine(t)
	const trials = 20
	reverted := 0
	for i := 0; i < trials; i++ {
		u := &User{Username: fmt.Sprintf("mia%d", i), Enabled: true}
		if err := repo.Create(u); err != nil {
			t.Fatalf("create: %v", err)
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			if err := e.ResetPassword(u.Username, "a-new-passphrase"); err != nil {
				t.Errorf("reset: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			_, _ = repo.Mutate(u.ID, func(u *User) error { u.Enabled = false; return nil })
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
		if !VerifyPassword("a-new-passphrase", final.PasswordHash) {
			t.Fatalf("trial %d: new password not stored", i)
		}
	}
	if reverted > 0 {
		t.Fatalf("admin disable reverted by a concurrent ResetPassword in %d/%d trials", reverted, trials)
	}
}

func TestDeleteRemovesRecordAndIndexTogether(t *testing.T) {
	_, repo := newTxTestEngine(t)
	u := &User{Username: "Nora", Enabled: true}
	if err := repo.Create(u); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Delete(u.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got, err := repo.GetByUsername("nora"); err != nil || got != nil {
		t.Fatalf("user still resolvable after delete: %v, %v", got, err)
	}
	if err := repo.Create(&User{Username: "nora", Enabled: true}); err != nil {
		t.Fatalf("name not reusable after delete: %v", err)
	}
	if err := repo.Delete("missing-id"); err != nil {
		t.Fatalf("deleting a missing user should be a no-op, got %v", err)
	}
}
