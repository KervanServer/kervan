package auth

import (
	"fmt"
	"sync"
	"testing"
	"time"

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

// raceDisable runs op concurrently with an admin disable of u, started
// after delay, and reports whether the disable survived.
func raceDisable(t *testing.T, repo *UserRepository, u *User, delay time.Duration, op func()) bool {
	t.Helper()
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		op()
	}()
	go func() {
		defer wg.Done()
		<-start
		time.Sleep(delay)
		_, _ = repo.Mutate(u.ID, func(u *User) error { u.Enabled = false; return nil })
	}()
	close(start)
	wg.Wait()
	final, err := repo.GetByID(u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return !final.Enabled
}

// An OIDC login syncing attributes must not revert a concurrent disable.
func TestProvisionExternalUserKeepsConcurrentDisable(t *testing.T) {
	e, repo := newTxTestEngine(t)
	const trials = 30
	reverted := 0
	for i := 0; i < trials; i++ {
		name := fmt.Sprintf("oli%d", i)
		u := &User{Username: name, AuthProvider: "oidc", ExternalID: "sub-" + name, Enabled: true}
		if err := repo.Create(u); err != nil {
			t.Fatalf("create: %v", err)
		}
		id := ExternalIdentity{Provider: "oidc", Subject: "sub-" + name, Username: name, Email: name + "@example.test"}
		if !raceDisable(t, repo, u, 0, func() { _, _ = e.ProvisionExternalUser(id, false) }) {
			reverted++
		}
	}
	if reverted > 0 {
		t.Fatalf("admin disable reverted by a concurrent OIDC sync in %d/%d trials", reverted, trials)
	}
}

// Renaming a group rewrites every member's references; a member's
// concurrent disable must survive it. Members are rewritten one by one after
// a single read, so the last of several members has the widest window.
func TestGroupRenameKeepsMemberConcurrentDisable(t *testing.T) {
	_, repo := newTxTestEngine(t)
	groups := NewGroupRepository(repo.store, repo)
	const trials, members = 25, 12
	reverted := 0
	for i := 0; i < trials; i++ {
		g := &Group{Name: fmt.Sprintf("team%d", i)}
		if err := groups.Create(g); err != nil {
			t.Fatalf("create group: %v", err)
		}
		var u *User
		for m := 0; m < members; m++ {
			u = &User{Username: fmt.Sprintf("pia%d-%02d", i, m), PrimaryGroup: g.Name, Enabled: true}
			if err := repo.Create(u); err != nil {
				t.Fatalf("create user: %v", err)
			}
		}
		renamed := *g
		renamed.Name = fmt.Sprintf("crew%d", i)
		// Sweep the disable's start across the rename so some trials land
		// between the member list read and the last member's rewrite.
		if !raceDisable(t, repo, u, time.Duration(i)*time.Millisecond, func() {
			if err := groups.Update(&renamed); err != nil {
				t.Errorf("rename: %v", err)
			}
		}) {
			reverted++
		}
		if final, _ := repo.GetByID(u.ID); final.PrimaryGroup != renamed.Name {
			t.Fatalf("trial %d: membership not rewritten: %q", i, final.PrimaryGroup)
		}
	}
	if reverted > 0 {
		t.Fatalf("member disable reverted by a concurrent group rename in %d/%d trials", reverted, trials)
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
