package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

func TestLoadCertificateInfo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cert.pem")
	notAfter := time.Now().UTC().Add(45 * 24 * time.Hour)
	writeTestCertificate(t, path, []string{"ftp.example.com", "example.com"}, notAfter)

	info, err := LoadCertificateInfo(path, time.Now().UTC())
	if err != nil {
		t.Fatalf("LoadCertificateInfo() error = %v", err)
	}
	if info.Status != "up" {
		t.Fatalf("Status = %q, want up", info.Status)
	}
	if len(info.DNSNames) != 2 {
		t.Fatalf("DNSNames len = %d", len(info.DNSNames))
	}
}

func TestLoadAutoCertInfo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ftp.example.com")
	notAfter := time.Now().UTC().Add(10 * 24 * time.Hour)
	writeTestCertificate(t, path, []string{"ftp.example.com"}, notAfter)

	info, err := LoadAutoCertInfo(dir, []string{"ftp.example.com"}, time.Now().UTC())
	if err != nil {
		t.Fatalf("LoadAutoCertInfo() error = %v", err)
	}
	if info.Status != "expiring" {
		t.Fatalf("Status = %q, want expiring", info.Status)
	}
}

func TestResolveCertificateInfoDisabled(t *testing.T) {
	info := ResolveCertificateInfo("", false, "", nil, time.Now().UTC())
	if got := info["status"]; got != "disabled" {
		t.Fatalf("status = %v, want disabled", got)
	}
}

func TestParseCertificateInfoAndResolveBranches(t *testing.T) {
	now := time.Now().UTC()
	expiredPath := filepath.Join(t.TempDir(), "expired.pem")
	writeTestCertificate(t, expiredPath, []string{"expired.example.com"}, now.Add(-1*time.Hour))

	raw, err := os.ReadFile(expiredPath)
	if err != nil {
		t.Fatalf("read expired cert: %v", err)
	}
	info, err := ParseCertificateInfo(raw, "file", now)
	if err != nil {
		t.Fatalf("parse certificate info: %v", err)
	}
	if info.Status != "expired" {
		t.Fatalf("expected expired status, got %q", info.Status)
	}

	if _, err := ParseCertificateInfo([]byte("not a cert"), "file", now); err == nil {
		t.Fatal("expected parsing non-certificate data to fail")
	}

	mapped := CertificateInfoMap(info)
	if mapped["serial_number"] == "" {
		t.Fatalf("expected certificate info map to include serial number, got %#v", mapped)
	}
	if empty := CertificateInfoMap(nil); len(empty) != 0 {
		t.Fatalf("expected nil certificate info map to be empty, got %#v", empty)
	}

	pending := ResolveCertificateInfo("", true, t.TempDir(), []string{"missing.example.com"}, now)
	if pending["status"] != "pending" {
		t.Fatalf("expected pending ACME info, got %#v", pending)
	}

	down := ResolveCertificateInfo(filepath.Join(t.TempDir(), "missing.pem"), false, "", nil, now)
	if down["status"] != "down" {
		t.Fatalf("expected down file cert info, got %#v", down)
	}
}

func writeTestCertificate(t *testing.T, path string, dnsNames []string, notAfter time.Time) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: dnsNames[0],
		},
		Issuer: pkix.Name{
			CommonName: "Kervan Test CA",
		},
		NotBefore:             time.Now().UTC().Add(-1 * time.Hour),
		NotAfter:              notAfter,
		DNSNames:              dnsNames,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	var raw []byte
	raw = append(raw, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: mustMarshalECPrivateKey(t, key)})...)
	raw = append(raw, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// Touch autocert import so the test guarantees cache compatibility assumptions compile.
	_ = autocert.DirCache(dirOf(path))
}

func mustMarshalECPrivateKey(t *testing.T, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	raw, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey() error = %v", err)
	}
	return raw
}

func dirOf(path string) string {
	return filepath.Dir(path)
}
