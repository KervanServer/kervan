package auth

import (
	"testing"

	"github.com/kervanserver/kervan/internal/store"
)

func TestUserRepositoryCreatePreservesDisabledFlag(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	repo := NewUserRepository(st)
	user := &User{
		Username:     "disabled-user",
		PasswordHash: "hash",
		HomeDir:      "/",
		Type:         UserTypeVirtual,
		Enabled:      false,
		Permissions:  DefaultUserPermissions(),
	}
	if err := repo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	saved, err := repo.GetByUsername("disabled-user")
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if saved == nil {
		t.Fatal("expected saved user")
	}
	if saved.Enabled {
		t.Fatal("expected disabled flag to be preserved")
	}
}

func TestUserRepositoryCreateTrimsUsernameForIndexLookups(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	repo := NewUserRepository(st)
	user := &User{
		Username:     "  alice  ",
		PasswordHash: "hash",
		HomeDir:      "/",
		Type:         UserTypeVirtual,
		Enabled:      true,
		Permissions:  DefaultUserPermissions(),
	}
	if err := repo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if user.Username != "alice" {
		t.Fatalf("expected username to be trimmed on create, got %q", user.Username)
	}

	saved, err := repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("lookup user: %v", err)
	}
	if saved == nil || saved.Username != "alice" {
		t.Fatalf("expected trimmed lookup to return user, got %#v", saved)
	}
}

func TestUserRepositoryUsernameIndexIsCaseInsensitive(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	repo := NewUserRepository(st)
	first := &User{
		Username:     "Alice",
		PasswordHash: "hash",
		HomeDir:      "/",
		Type:         UserTypeVirtual,
		Enabled:      true,
		Permissions:  DefaultUserPermissions(),
	}
	if err := repo.Create(first); err != nil {
		t.Fatalf("create first user: %v", err)
	}

	found, err := repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("lookup case-insensitive user: %v", err)
	}
	if found == nil || found.Username != "Alice" {
		t.Fatalf("expected case-insensitive lookup to return original user, got %#v", found)
	}

	second := &User{
		Username:     "ALICE",
		PasswordHash: "hash",
		HomeDir:      "/",
		Type:         UserTypeVirtual,
		Enabled:      true,
		Permissions:  DefaultUserPermissions(),
	}
	if err := repo.Create(second); err == nil {
		t.Fatal("expected duplicate username with different case to be rejected")
	}
}

func TestUserRepositoryUpdateRefreshesUsernameIndex(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	repo := NewUserRepository(st)
	user := &User{
		Username:     "alice",
		PasswordHash: "hash",
		HomeDir:      "/",
		Type:         UserTypeVirtual,
		Enabled:      true,
		Permissions:  DefaultUserPermissions(),
	}
	if err := repo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	user.Username = "bob"
	if err := repo.Update(user); err != nil {
		t.Fatalf("update user: %v", err)
	}

	oldLookup, err := repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("lookup old username: %v", err)
	}
	if oldLookup != nil {
		t.Fatalf("expected old username lookup to be cleared, got %#v", oldLookup)
	}

	newLookup, err := repo.GetByUsername("bob")
	if err != nil {
		t.Fatalf("lookup new username: %v", err)
	}
	if newLookup == nil || newLookup.ID != user.ID {
		t.Fatalf("expected new username lookup to return updated user, got %#v", newLookup)
	}
}
