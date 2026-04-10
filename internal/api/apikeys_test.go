package api

import (
	"strings"
	"testing"

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
