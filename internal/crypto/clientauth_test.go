package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTestCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWithClientAuth(t *testing.T) {
	base := &tls.Config{MinVersion: tls.VersionTLS12}
	if got, err := WithClientAuth(base, "none", ""); err != nil || got != base {
		t.Fatalf("none must return base unchanged: %v", err)
	}
	if _, err := WithClientAuth(base, "require", ""); err == nil {
		t.Fatal("require without CA must fail")
	}
	if _, err := WithClientAuth(base, "bogus", "x"); err == nil {
		t.Fatal("unknown mode must fail")
	}
	ca := writeTestCA(t)
	got, err := WithClientAuth(base, "require", ca)
	if err != nil {
		t.Fatal(err)
	}
	if got == base || got.ClientAuth != tls.RequireAndVerifyClientCert || got.ClientCAs == nil {
		t.Fatalf("require not applied: %+v", got.ClientAuth)
	}
	if base.ClientAuth != tls.NoClientCert {
		t.Fatal("base config (shared with WebUI) must not be mutated")
	}
	req, err := WithClientAuth(base, "request", ca)
	if err != nil || req.ClientAuth != tls.VerifyClientCertIfGiven {
		t.Fatalf("request mode: %v", err)
	}
}
