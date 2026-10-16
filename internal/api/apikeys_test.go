package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/store"
)

func TestAPIKeyRepositoryCreateListDelete(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	repo := NewAPIKeyRepository(st)
	token, created, err := repo.Create("user-1", "CI key", "read-only")
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	if !strings.HasPrefix(token, "kervan_") {
		t.Fatalf("expected generated token to start with kervan_, got %q", token)
	}
	if created == nil {
		t.Fatal("expected created record")
	}
	if created.Hash == "" {
		t.Fatal("expected hash to be set")
	}
	if created.Prefix == "" {
		t.Fatal("expected prefix to be set")
	}
	if created.Hash == token {
		t.Fatal("expected stored hash to differ from plaintext token")
	}
	resolved, err := repo.GetByToken(token)
	if err != nil {
		t.Fatalf("resolve api key: %v", err)
	}
	if resolved == nil || resolved.ID != created.ID {
		t.Fatalf("unexpected resolved key: %#v", resolved)
	}

	list, err := repo.ListByUser("user-1")
	if err != nil {
		t.Fatalf("list keys: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one key, got %d", len(list))
	}
	if list[0].ID != created.ID {
		t.Fatalf("unexpected key id: %s", list[0].ID)
	}

	otherUserList, err := repo.ListByUser("user-2")
	if err != nil {
		t.Fatalf("list other user keys: %v", err)
	}
	if len(otherUserList) != 0 {
		t.Fatalf("expected no keys for other user, got %d", len(otherUserList))
	}

	usedAt := time.Now().UTC().Round(time.Second)
	if err := repo.UpdateLastUsed(created.ID, usedAt); err != nil {
		t.Fatalf("update last used: %v", err)
	}
	listAfterTouch, err := repo.ListByUser("user-1")
	if err != nil {
		t.Fatalf("list after touch: %v", err)
	}
	if len(listAfterTouch) != 1 || listAfterTouch[0].LastUsedAt == nil {
		t.Fatalf("expected last used timestamp to be persisted, got %#v", listAfterTouch)
	}
	if !listAfterTouch[0].LastUsedAt.Equal(usedAt) {
		t.Fatalf("expected last used=%s, got %s", usedAt, listAfterTouch[0].LastUsedAt)
	}

	if err := repo.Delete("user-2", created.ID); err == nil {
		t.Fatal("expected delete to fail for another user")
	}
	if err := repo.Delete("user-1", created.ID); err != nil {
		t.Fatalf("delete key: %v", err)
	}
	listAfterDelete, err := repo.ListByUser("user-1")
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(listAfterDelete) != 0 {
		t.Fatalf("expected no keys after delete, got %d", len(listAfterDelete))
	}
}

func TestAPIKeyRepositoryRejectsUnknownPermissions(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	repo := NewAPIKeyRepository(st)
	if _, _, err := repo.Create("user-1", "bad key", "admin"); err == nil {
		t.Fatal("expected invalid permissions to be rejected")
	}
}

func TestAPIKeyRepositoryCanonicalizesScopedPermissions(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	repo := NewAPIKeyRepository(st)
	_, created, err := repo.Create("user-1", "scoped", "files:write, files:read, share:*")
	if err != nil {
		t.Fatalf("create scoped api key: %v", err)
	}
	if created.Permissions != "files:read,files:write,share:read,share:write" {
		t.Fatalf("unexpected canonical permissions: %q", created.Permissions)
	}
}

// storeFileID identifies the current store file. Every store write replaces
// the file with an atomic rename, so a new inode means the store was
// rewritten.
func storeFileID(t *testing.T, dir string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(filepath.Join(dir, "kervan-store.json"))
	if err != nil {
		t.Fatalf("stat store file: %v", err)
	}
	return info
}

// R2: requests through an API key must not each rewrite the whole store
// file. The last-used time is persisted at most once per
// apiKeyLastUsedResolution.
func TestAPIKeyUpdateLastUsedIsThrottled(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	repo := NewAPIKeyRepository(st)
	_, created, err := repo.Create("user-1", "CI key", "read-only")
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	lastUsed := func() time.Time {
		t.Helper()
		keys, err := repo.ListByUser("user-1")
		if err != nil || len(keys) != 1 {
			t.Fatalf("list keys: %v %#v", err, keys)
		}
		if keys[0].LastUsedAt == nil {
			return time.Time{}
		}
		return *keys[0].LastUsedAt
	}

	base := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	if err := repo.UpdateLastUsed(created.ID, base); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if got := lastUsed(); !got.Equal(base) {
		t.Fatalf("first use stored %v, want %v", got, base)
	}

	// Many uses inside the window: no store write at all.
	before := storeFileID(t, dir)
	for i := 1; i <= 100; i++ {
		if err := repo.UpdateLastUsed(created.ID, base.Add(time.Duration(i)*500*time.Millisecond)); err != nil {
			t.Fatalf("use %d: %v", i, err)
		}
	}
	if after := storeFileID(t, dir); !os.SameFile(before, after) {
		t.Fatal("uses within the resolution window rewrote the store file")
	}
	if got := lastUsed(); !got.Equal(base) {
		t.Fatalf("stored time moved to %v inside the window", got)
	}

	// Once the stored time is a full window old, the next use is written.
	next := base.Add(apiKeyLastUsedResolution)
	if err := repo.UpdateLastUsed(created.ID, next); err != nil {
		t.Fatalf("use after window: %v", err)
	}
	if got := lastUsed(); !got.Equal(next) {
		t.Fatalf("use after the window stored %v, want %v", got, next)
	}
	if after := storeFileID(t, dir); os.SameFile(before, after) {
		t.Fatal("use after the window did not write the store")
	}

	// An older timestamp (e.g. a delayed request) never moves the time back.
	if err := repo.UpdateLastUsed(created.ID, base); err != nil {
		t.Fatalf("stale use: %v", err)
	}
	if got := lastUsed(); !got.Equal(next) {
		t.Fatalf("a stale use moved last-used back to %v", got)
	}
}

// A key deleted while a request through it is in flight is not recreated
// by the last-used write.
func TestAPIKeyUpdateLastUsedDoesNotResurrectDeletedKey(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	repo := NewAPIKeyRepository(st)
	_, created, err := repo.Create("user-1", "CI key", "read-only")
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	if err := repo.Delete("user-1", created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := repo.UpdateLastUsed(created.ID, time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("UpdateLastUsed on a deleted key = %v, want store.ErrNotFound", err)
	}
	if keys, _ := repo.ListByUser("user-1"); len(keys) != 0 {
		t.Fatalf("deleted key came back: %#v", keys)
	}
}

// End to end: a burst of authenticated requests through one API key writes
// the store once, not once per request.
func TestAPIKeyRequestsDoNotRewriteStorePerRequest(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	users := auth.NewUserRepository(st)
	engine := auth.NewEngine(users, "argon2id", 5, 15*time.Minute)
	alice, err := engine.CreateUser("alice", "StrongPass123!", "/", false)
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	srv := &Server{auth: engine, users: users, apiKeys: NewAPIKeyRepository(st)}
	token, _, err := srv.apiKeys.Create(alice.ID, "CI key", "read-write")
	if err != nil {
		t.Fatalf("Create api key: %v", err)
	}
	call := func() {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/server/status", nil)
		req.Header.Set("Authorization", "ApiKey "+token)
		rec := httptest.NewRecorder()
		srv.withAuth(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }).ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("api key request: %d %s", rec.Code, rec.Body.String())
		}
	}
	call() // first use is recorded
	before := storeFileID(t, dir)
	for i := 0; i < 50; i++ {
		call()
	}
	if after := storeFileID(t, dir); !os.SameFile(before, after) {
		t.Fatal("repeated API-key requests rewrote the store file")
	}
}
