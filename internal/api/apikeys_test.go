package api

import (
	"strings"
	"testing"
	"time"

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

	repo := newAPIKeyRepository(st)
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

	repo := newAPIKeyRepository(st)
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

	repo := newAPIKeyRepository(st)
	_, created, err := repo.Create("user-1", "scoped", "files:write, files:read, share:*")
	if err != nil {
		t.Fatalf("create scoped api key: %v", err)
	}
	if created.Permissions != "files:read,files:write,share:read,share:write" {
		t.Fatalf("unexpected canonical permissions: %q", created.Permissions)
	}
}
