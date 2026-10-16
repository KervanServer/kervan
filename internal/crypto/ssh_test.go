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

// Contract: an SSH host private key must be readable only by its owner. The
// writers pass 0o600, but os.WriteFile applies its perm argument only when it
// CREATES the file — regenerating over an existing key (`kervan keygen
// --force`) left a pre-existing/restored key group/other-readable.
func TestGenerateHostKeyTightensPermissionsOnRegenerate(t *testing.T) {
	dir := t.TempDir()

	for _, tc := range []struct {
		name string
		gen  func(string) error
	}{
		{"ed25519", GenerateED25519HostKey},
		{"rsa", func(p string) error { return GenerateRSAHostKey(p, 2048) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+"_host_key")
			// Pre-existing, world-readable key (restored from backup or
			// created by another tool).
			if err := os.WriteFile(path, []byte("stale key material"), 0o644); err != nil {
				t.Fatalf("seed existing key: %v", err)
			}
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatalf("chmod seed: %v", err)
			}

			if err := tc.gen(path); err != nil {
				t.Fatalf("generate host key: %v", err)
			}

			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat regenerated key: %v", err)
			}
			if mode := info.Mode().Perm(); mode&0o077 != 0 {
				t.Fatalf("regenerated host private key has mode %04o; it must not grant "+
					"group/other access", mode)
			}
			// Control: the regenerated key still loads and replaced the stale
			// material rather than reusing it.
			if _, err := LoadSigner(path); err != nil {
				t.Fatalf("control: regenerated key must still load: %v", err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("control: read regenerated key: %v", err)
			}
			if len(raw) > 0 && len(raw) >= len("stale key material") && string(raw[:len("stale key material")]) == "stale key material" {
				t.Fatalf("control: stale key material survived regeneration")
			}
		})
	}
}

// Control: a brand-new host key is private-only.
func TestGenerateHostKeyFreshFileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh_host_key")
	if err := GenerateED25519HostKey(path); err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Fatalf("freshly generated host key has mode %04o; it must not grant group/other access", mode)
	}
}
