package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/store"
)

// storeFileInfo identifies the current store file. Every store write
// replaces it with an atomic rename, so a new inode means a write.
func storeFileInfo(t *testing.T, dir string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(filepath.Join(dir, "kervan-store.json"))
	if err != nil {
		t.Fatalf("stat store file: %v", err)
	}
	return fi
}

func newLastLoginRepo(t *testing.T) (*UserRepository, string, *User) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	repo := NewUserRepository(st)
	u := &User{Username: "dana", HomeDir: "/", Type: UserTypeVirtual, Enabled: true, Permissions: DefaultUserPermissions()}
	if err := repo.Create(u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return repo, dir, u
}

// ageLastLogin moves the stored last-login time back by d, as if the
// previous sign-in had happened that long ago.
func ageLastLogin(t *testing.T, repo *UserRepository, id string, d time.Duration) {
	t.Helper()
	if _, err := repo.Mutate(id, func(u *User) error {
		if u.LastLoginAt == nil {
			t.Fatal("no last-login time to age")
		}
		old := u.LastLoginAt.Add(-d)
		u.LastLoginAt = &old
		return nil
	}); err != nil {
		t.Fatalf("age last login: %v", err)
	}
}

// R2: repeated successful sign-ins of an account in good standing rewrite
// the store at most once per lastLoginResolution.
func TestUpdateLastLoginWritesOncePerMinute(t *testing.T) {
	repo, dir, u := newLastLoginRepo(t)

	if err := repo.UpdateLastLogin(u.ID); err != nil {
		t.Fatalf("first sign-in: %v", err)
	}
	first, _ := repo.GetByID(u.ID)
	if first.LastLoginAt == nil {
		t.Fatal("first sign-in did not record the time")
	}

	before := storeFileInfo(t, dir)
	for i := 0; i < 20; i++ {
		if err := repo.UpdateLastLogin(u.ID); err != nil {
			t.Fatalf("sign-in %d: %v", i, err)
		}
	}
	if after := storeFileInfo(t, dir); !os.SameFile(before, after) {
		t.Fatal("repeated sign-ins within a minute rewrote the store")
	}
	if got, _ := repo.GetByID(u.ID); !got.LastLoginAt.Equal(*first.LastLoginAt) {
		t.Fatalf("last-login time moved inside the window: %v -> %v", first.LastLoginAt, got.LastLoginAt)
	}

	// Once the stored time is a full window old, the next sign-in writes.
	ageLastLogin(t, repo, u.ID, lastLoginResolution)
	aged, _ := repo.GetByID(u.ID)
	before = storeFileInfo(t, dir)
	if err := repo.UpdateLastLogin(u.ID); err != nil {
		t.Fatalf("sign-in after the window: %v", err)
	}
	if after := storeFileInfo(t, dir); os.SameFile(before, after) {
		t.Fatal("sign-in after the window did not write the store")
	}
	if got, _ := repo.GetByID(u.ID); !got.LastLoginAt.After(*aged.LastLoginAt) {
		t.Fatalf("sign-in after the window kept the old time %v", got.LastLoginAt)
	}
}

// A sign-in after a failed attempt must clear the failure counter at once,
// even within the window, so earlier failures never count towards a later
// lockout; the same goes for an expired lockout.
func TestUpdateLastLoginAfterFailureResetsImmediately(t *testing.T) {
	repo, dir, u := newLastLoginRepo(t)
	if err := repo.UpdateLastLogin(u.ID); err != nil {
		t.Fatalf("first sign-in: %v", err)
	}

	// A failed attempt in the same minute.
	if _, err := repo.Mutate(u.ID, func(u *User) error { u.FailedLogins = 3; return nil }); err != nil {
		t.Fatal(err)
	}
	before := storeFileInfo(t, dir)
	if err := repo.UpdateLastLogin(u.ID); err != nil {
		t.Fatalf("sign-in after failure: %v", err)
	}
	if after := storeFileInfo(t, dir); os.SameFile(before, after) {
		t.Fatal("sign-in after a failure did not write the store")
	}
	if got, _ := repo.GetByID(u.ID); got.FailedLogins != 0 {
		t.Fatalf("sign-in after a failure left FailedLogins = %d", got.FailedLogins)
	}

	// A lockout that has expired is cleared by the next sign-in, too.
	past := time.Now().UTC().Add(-time.Second)
	if _, err := repo.Mutate(u.ID, func(u *User) error { u.LockedUntil = &past; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateLastLogin(u.ID); err != nil {
		t.Fatalf("sign-in after lockout: %v", err)
	}
	if got, _ := repo.GetByID(u.ID); got.LockedUntil != nil {
		t.Fatalf("sign-in left LockedUntil = %v", got.LockedUntil)
	}
}

// End to end through the engine: failures, then a correct password in the
// same minute, then more failures. The counter restarts after the success,
// so the account is not locked by failures from before it.
func TestSuccessfulLoginResetsLockoutCountWithinWindow(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	repo := NewUserRepository(st)
	engine := NewEngine(repo, "bcrypt", 3, time.Hour)
	const pass = "correct-horse-1" // test fixture
	if _, err := engine.CreateUser("erin", pass, "/", false); err != nil {
		t.Fatal(err)
	}
	signIn := func(pw string) error {
		u, err := engine.Authenticate(t.Context(), "erin", pw)
		if err == nil {
			return engine.RecordSuccessfulLogin(u.ID)
		}
		return err
	}
	if err := signIn(pass); err != nil { // records LastLoginAt now
		t.Fatalf("first sign-in: %v", err)
	}
	for i := 0; i < 2; i++ { // 2 of 3 allowed failures
		if err := signIn("wrong"); err == nil {
			t.Fatal("wrong password accepted")
		}
	}
	if err := signIn(pass); err != nil { // same minute: must still reset
		t.Fatalf("sign-in after failures: %v", err)
	}
	for i := 0; i < 2; i++ { // 2 more failures: still under the limit
		_ = signIn("wrong")
	}
	if err := signIn(pass); err != nil {
		t.Fatalf("account locked by failures from before a successful sign-in: %v", err)
	}
}
