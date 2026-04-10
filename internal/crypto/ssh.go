package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

func EnsureHostKeys(dir string) (privateKeyPath string, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
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
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	return os.WriteFile(path, pem.EncodeToMemory(block), 0o600)
}

func GenerateRSAHostKey(path string, bits int) error {
	if bits < 2048 {
		return errors.New("bits must be >= 2048")
	}
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return err
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}
	return os.WriteFile(path, pem.EncodeToMemory(block), 0o600)
}

func LoadSigner(path string) (ssh.Signer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(raw)
}
