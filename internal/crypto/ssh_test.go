package crypto

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureHostKeysCreatesAndReusesKey(t *testing.T) {
	dir := t.TempDir()

	firstPath, err := EnsureHostKeys(dir)
	if err != nil {
		t.Fatalf("ensure host keys first call: %v", err)
	}
	if _, err := os.Stat(firstPath); err != nil {
		t.Fatalf("expected generated host key to exist: %v", err)
	}

	secondPath, err := EnsureHostKeys(dir)
	if err != nil {
		t.Fatalf("ensure host keys second call: %v", err)
	}
	if firstPath != secondPath {
		t.Fatalf("expected ensure host keys to reuse existing path: %q vs %q", firstPath, secondPath)
	}
}

func TestGenerateRSAHostKeyValidationAndLoadSigner(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateRSAHostKey(filepath.Join(dir, "too-small"), 1024); err == nil {
		t.Fatal("expected small rsa key generation to fail")
	}

	keyPath := filepath.Join(dir, "ssh_host_ed25519_key")
	if err := GenerateED25519HostKey(keyPath); err != nil {
		t.Fatalf("generate ed25519 host key: %v", err)
	}
	signer, err := LoadSigner(keyPath)
	if err != nil {
		t.Fatalf("load signer: %v", err)
	}
	if signer == nil {
		t.Fatal("expected signer to be loaded")
	}

	if _, err := LoadSigner(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected missing signer load to fail with os.ErrNotExist, got %v", err)
	}
}
