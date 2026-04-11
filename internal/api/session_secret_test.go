package api

import (
	"bytes"
	"testing"

	"github.com/kervanserver/kervan/internal/store"
)

func TestLoadOrCreateSessionSecretPersistsAcrossLoads(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	first, err := loadOrCreateSessionSecret(st)
	if err != nil {
		t.Fatalf("first loadOrCreateSessionSecret: %v", err)
	}
	second, err := loadOrCreateSessionSecret(st)
	if err != nil {
		t.Fatalf("second loadOrCreateSessionSecret: %v", err)
	}

	if len(first) < 32 {
		t.Fatalf("expected at least 32 bytes, got %d", len(first))
	}
	if !bytes.Equal(first, second) {
		t.Fatal("expected persisted session secret to stay stable")
	}
}

func TestLoadOrCreateSessionSecretWithoutStoreGeneratesValue(t *testing.T) {
	secret, err := loadOrCreateSessionSecret(nil)
	if err != nil {
		t.Fatalf("loadOrCreateSessionSecret(nil): %v", err)
	}
	if len(secret) < 32 {
		t.Fatalf("expected at least 32 bytes, got %d", len(secret))
	}
}
