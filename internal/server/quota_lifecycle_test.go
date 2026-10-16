package server

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/quota"
	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/store"
	"github.com/kervanserver/kervan/internal/vfs"
)

// memoryBackendOf returns the in-memory world buildUserFS hands username, so
// a test can change files "outside Kervan".
func memoryBackendOf(t *testing.T, app *App, username string) *memory.Backend {
	t.Helper()
	cached, ok := app.memoryFS.Load(username)
	if !ok {
		t.Fatalf("no memory backend for %q yet", username)
	}
	return cached.(*memory.Backend)
}

func seedBackend(t *testing.T, fsys vfs.FileSystem, name string, size int) {
	t.Helper()
	f, err := fsys.Open(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(make([]byte, size)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// writeErr writes size bytes to a new file and returns the write error.
func writeErr(t *testing.T, fsys vfs.FileSystem, name string, size int) error {
	t.Helper()
	f, err := fsys.Open(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(make([]byte, size))
	_ = f.Close()
	return werr
}

// Finding 1: quota.check_interval <= 0 must not mean "re-measure on every
// use". buildUserFS runs per connection and per API file request, so that
// re-walked the user's whole tree on each one.
func TestQuotaCheckIntervalNonPositiveUsesDefault(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second} {
		app := &App{cfg: buildMemoryApp(t).cfg}
		app.cfg.Quota.CheckInterval = d
		if got := app.quotaCheckInterval(); got != defaultQuotaCheckInterval {
			t.Fatalf("check_interval %v -> %v, want the %v default", d, got, defaultQuotaCheckInterval)
		}
	}
	app := buildMemoryApp(t)
	app.cfg.Quota.CheckInterval = 5 * time.Minute
	if got := app.quotaCheckInterval(); got != 5*time.Minute {
		t.Fatalf("positive check_interval not kept: %v", got)
	}

	// End to end: with check_interval 0, a second request inside the default
	// interval does not re-walk storage, so a file added outside Kervan is
	// not yet counted.
	app, user := appWithSharedQuota(t, 10)
	app.cfg.Quota.CheckInterval = 0
	if _, err := app.buildUserFS(user); err != nil {
		t.Fatal(err)
	}
	seedBackend(t, memoryBackendOf(t, app, user.Username), "/outside.bin", 8)
	fsys, err := app.buildUserFS(user)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeErr(t, fsys, "/in.bin", 10); err != nil {
		t.Fatalf("check_interval 0 re-measured storage on the next request: %v", err)
	}
}

// Finding 2: a write handle a session never closed (client vanished
// mid-upload) must not stop re-measurement forever. Releasing the session
// ends its outstanding changes.
func TestReleasedSessionUnblocksQuotaRemeasure(t *testing.T) {
	app, user := appWithSharedQuota(t, 10)
	app.cfg.Quota.CheckInterval = time.Nanosecond // re-measure whenever idle

	leaky, err := app.buildUserFS(user)
	if err != nil {
		t.Fatal(err)
	}
	f, err := leaky.Open("/partial.bin", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(make([]byte, 4)); err != nil {
		t.Fatal(err)
	}
	// f is never closed. 6 bytes appear outside Kervan.
	seedBackend(t, memoryBackendOf(t, app, user.Username), "/outside.bin", 6)

	// While the handle is open, sign-ins keep the incremental figure (4):
	// the walk cannot see the reserved bytes.
	fsys, err := app.buildUserFS(user)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeErr(t, fsys, "/probe.bin", 6); err != nil { // 4 + 6 = 10
		t.Fatalf("re-measured while a write handle was open: %v", err)
	}
	if err := fsys.Remove("/probe.bin"); err != nil {
		t.Fatal(err)
	}

	vfs.ReleaseFS(leaky) // the connection ended without closing f

	// Now the next sign-in re-measures storage: 6 bytes outside Kervan (the
	// unclosed upload never reached storage), so 5 more do not fit.
	fsys, err = app.buildUserFS(user)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeErr(t, fsys, "/more.bin", 5); !errors.Is(err, quota.ErrStorageExceeded) {
		t.Fatalf("usage was not re-measured after the leaked handle's session ended: err = %v", err)
	}
	_ = f.Close() // a late Close must not end the change a second time
}

// appWithRepoQuota is appWithSharedQuota plus a user repository, so users
// carry real IDs and can be deleted.
func appWithRepoQuota(t *testing.T, maxStorage int64) (*App, *auth.UserRepository) {
	t.Helper()
	app, _ := appWithSharedQuota(t, maxStorage)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	app.authRepo = auth.NewUserRepository(st)
	return app, app.authRepo
}

func newCappedUser(t *testing.T, repo *auth.UserRepository, name string) *auth.User {
	t.Helper()
	u := &auth.User{Username: name, HomeDir: "/", Type: auth.UserTypeVirtual, Permissions: auth.DefaultUserPermissions(), PrimaryGroup: "capped", Enabled: true}
	if err := repo.Create(u); err != nil {
		t.Fatal(err)
	}
	return u
}

// Finding 3a: a deleted account re-created under the same name must not
// inherit the old account's usage figures.
func TestRecreatedUserGetsFreshQuotaTracker(t *testing.T) {
	app, repo := appWithRepoQuota(t, 10)
	old := newCappedUser(t, repo, "dave")
	fsys, err := app.buildUserFS(old)
	if err != nil {
		t.Fatal(err)
	}
	writeViaFS(t, fsys, "/a.bin", "12345678") // 8 of 10 bytes

	// The account is deleted and its files cleaned up, then an account
	// with the same name is created.
	if err := repo.Delete(old.ID); err != nil {
		t.Fatal(err)
	}
	if err := memoryBackendOf(t, app, "dave").Remove("/a.bin"); err != nil {
		t.Fatal(err)
	}
	fresh := newCappedUser(t, repo, "dave")
	fsys, err = app.buildUserFS(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeErr(t, fsys, "/b.bin", 10); err != nil {
		t.Fatalf("re-created account inherited the deleted account's usage: %v", err)
	}
}

// Finding 3b: renaming an account keeps one quota for its sessions under
// the old and the new name.
func TestRenamedUserKeepsSharedQuota(t *testing.T) {
	app, repo := appWithRepoQuota(t, 10)
	// The local backend, so both names see the same files.
	local := buildLocalApp(t)
	app.cfg.Storage = local.cfg.Storage

	user := newCappedUser(t, repo, "erin")
	before, err := app.buildUserFS(user)
	if err != nil {
		t.Fatal(err)
	}
	renamed := *user
	renamed.Username = "erin2"
	after, err := app.buildUserFS(&renamed)
	if err != nil {
		t.Fatal(err)
	}
	writeViaFS(t, before, "/a.bin", "12345678")
	if err := writeErr(t, after, "/b.bin", 8); !errors.Is(err, quota.ErrStorageExceeded) {
		t.Fatalf("sessions before and after a rename did not share the quota: err = %v", err)
	}
}

// Finding 3c: trackers of deleted accounts are dropped from the registry.
func TestPruneQuotaTrackersDropsDeletedUsers(t *testing.T) {
	app, repo := appWithRepoQuota(t, 10)
	kept := newCappedUser(t, repo, "frank")
	gone := newCappedUser(t, repo, "gina")
	for _, u := range []*auth.User{kept, gone} {
		if _, err := app.buildUserFS(u); err != nil {
			t.Fatal(err)
		}
	}
	if n := app.quotas.Len(); n != 2 {
		t.Fatalf("registry holds %d trackers, want 2", n)
	}
	if err := repo.Delete(gone.ID); err != nil {
		t.Fatal(err)
	}
	if dropped := app.pruneQuotaTrackers(); dropped != 1 {
		t.Fatalf("pruned %d trackers, want 1", dropped)
	}
	if n := app.quotas.Len(); n != 1 {
		t.Fatalf("registry holds %d trackers after pruning, want 1", n)
	}
}
