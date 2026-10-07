package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/store"
)

func TestProvisionExternalUser(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	repo := NewUserRepository(st)
	e := NewEngine(repo, "bcrypt", 5, time.Minute)
	yes, no := true, false

	id := ExternalIdentity{Provider: AuthProviderOIDC, Subject: "sub-1", Username: "ann", Email: "ann@example.com", HomeDir: "/ann", PrimaryGroup: "eng", SyncGroups: true}
	if _, err := e.ProvisionExternalUser(id, false); !errors.Is(err, ErrNotProvisioned) {
		t.Fatalf("auto_create=false: %v", err)
	}
	u, err := e.ProvisionExternalUser(id, true)
	if err != nil {
		t.Fatal(err)
	}
	if u.Type != UserTypeVirtual || u.AuthProvider != AuthProviderOIDC || u.HomeDir != "/ann" || u.PrimaryGroup != "eng" {
		t.Fatalf("created user = %+v", u)
	}

	// The role follows admin groups when they are configured...
	id.Admin = &yes
	id.PrimaryGroup = "ops"
	if u, _ = e.ProvisionExternalUser(id, true); u.Type != UserTypeAdmin || u.PrimaryGroup != "ops" {
		t.Fatalf("sync = %s %q", u.Type, u.PrimaryGroup)
	}
	id.Admin = &no
	if u, _ = e.ProvisionExternalUser(id, true); u.Type != UserTypeVirtual {
		t.Fatal("demotion not applied")
	}
	// ...and is left alone when they are not.
	u.Type = UserTypeAdmin
	_ = repo.Update(u)
	id.Admin = nil
	if u, _ = e.ProvisionExternalUser(id, true); u.Type != UserTypeAdmin {
		t.Fatal("manually granted admin role overwritten")
	}

	// A different subject cannot claim the same username.
	other := id
	other.Subject = "sub-2"
	if _, err := e.ProvisionExternalUser(other, true); !errors.Is(err, ErrAccountConflict) {
		t.Fatalf("subject takeover: %v", err)
	}

	// A local account cannot be taken over through OIDC.
	if _, err := e.CreateUser("bob", "StrongPass123!", "/", true); err != nil {
		t.Fatal(err)
	}
	takeover := ExternalIdentity{Provider: AuthProviderOIDC, Subject: "x", Username: "bob"}
	if _, err := e.ProvisionExternalUser(takeover, true); !errors.Is(err, ErrAccountConflict) {
		t.Fatalf("local takeover: %v", err)
	}

	// OIDC accounts cannot log in with a password.
	if _, err := e.Authenticate(context.Background(), "ann", "!external"); err == nil {
		t.Fatal("password login accepted for an OIDC account")
	}

	// Disabled accounts are refused.
	u.Enabled = false
	_ = repo.Update(u)
	if _, err := e.ProvisionExternalUser(id, true); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("disabled: %v", err)
	}
}
