package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

func EnsureHostKeys(dir string) (privateKeyPath string, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create host key directory %s: %w", dir, err)
	}
	edPath := filepath.Join(dir, "ssh_host_ed25519_key")
	if _, statErr := os.Stat(edPath); statErr == nil {
		return edPath, nil
	}
	return edPath, GenerateED25519HostKey(edPath)
}

func GenerateED25519HostKey(path string) error {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate ed25519 key for %s: %w", path, err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal ed25519 key for %s: %w", path, err)
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return fmt.Errorf("write ed25519 key file %s: %w", path, err)
	}
	return nil
}

func GenerateRSAHostKey(path string, bits int) error {
	if bits < 2048 {
		return errors.New("bits must be >= 2048")
	}
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return fmt.Errorf("generate rsa key for %s: %w", path, err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return fmt.Errorf("write rsa key file %s: %w", path, err)
	}
	return nil
}

func LoadSigner(path string) (ssh.Signer, error) {
	// #nosec G304 -- host key path comes from server configuration controlled by operators.
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key %s: %w", path, err)
	}
	signer, err := ssh.ParsePrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("parse private key %s: %w", path, err)
	}
	return signer, nil
}
