package crypto

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

type CertificateInfo struct {
	Source           string    `json:"source"`
	Status           string    `json:"status"`
	Subject          string    `json:"subject,omitempty"`
	Issuer           string    `json:"issuer,omitempty"`
	DNSNames         []string  `json:"dns_names,omitempty"`
	NotBefore        time.Time `json:"not_before,omitempty"`
	NotAfter         time.Time `json:"not_after,omitempty"`
	ExpiresInSeconds int64     `json:"expires_in_seconds,omitempty"`
	SerialNumber     string    `json:"serial_number,omitempty"`
}

func LoadCertificateInfo(certFile string, now time.Time) (*CertificateInfo, error) {
	raw, err := os.ReadFile(certFile)
	if err != nil {
		return nil, err
	}
	return ParseCertificateInfo(raw, "file", now)
}

func LoadAutoCertInfo(cacheDir string, domains []string, now time.Time) (*CertificateInfo, error) {
	cache := autocert.DirCache(cacheDir)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	for _, domain := range domains {
		domain = strings.TrimSpace(domain)
		if domain == "" {
			continue
		}
		for _, key := range []string{domain, domain + "+rsa"} {
			raw, err := cache.Get(ctx, key)
			if err != nil {
				continue
			}
			info, parseErr := ParseCertificateInfo(raw, "acme", now)
			if parseErr != nil {
				continue
			}
			return info, nil
		}
	}
	return nil, os.ErrNotExist
}

func ParseCertificateInfo(raw []byte, source string, now time.Time) (*CertificateInfo, error) {
	var blocks [][]byte
	rest := raw
	for len(rest) > 0 {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			blocks = append(blocks, block.Bytes)
		}
		rest = next
	}
	if len(blocks) == 0 {
		return nil, errors.New("no certificate found")
	}
	leaf, err := x509.ParseCertificate(blocks[0])
	if err != nil {
		return nil, err
	}
	info := &CertificateInfo{
		Source:           source,
		Subject:          leaf.Subject.String(),
		Issuer:           leaf.Issuer.String(),
		DNSNames:         append([]string(nil), leaf.DNSNames...),
		NotBefore:        leaf.NotBefore,
		NotAfter:         leaf.NotAfter,
		ExpiresInSeconds: int64(leaf.NotAfter.Sub(now).Seconds()),
		SerialNumber:     leaf.SerialNumber.Text(16),
	}
	switch {
	case now.After(leaf.NotAfter):
		info.Status = "expired"
	case leaf.NotAfter.Before(now.Add(30 * 24 * time.Hour)):
		info.Status = "expiring"
	default:
		info.Status = "up"
	}
	return info, nil
}

func CertificateInfoMap(info *CertificateInfo) map[string]any {
	if info == nil {
		return map[string]any{}
	}
	return map[string]any{
		"source":             info.Source,
		"status":             info.Status,
		"subject":            info.Subject,
		"issuer":             info.Issuer,
		"dns_names":          info.DNSNames,
		"not_before":         info.NotBefore,
		"not_after":          info.NotAfter,
		"expires_in_seconds": info.ExpiresInSeconds,
		"serial_number":      strings.ToUpper(info.SerialNumber),
	}
}

func ResolveCertificateInfo(certFile string, autoCertEnabled bool, autoCertDir string, domains []string, now time.Time) map[string]any {
	switch {
	case autoCertEnabled:
		info, err := LoadAutoCertInfo(autoCertDir, domains, now)
		if err == nil {
			out := CertificateInfoMap(info)
			out["domains"] = append([]string(nil), domains...)
			return out
		}
		return map[string]any{
			"source":    "acme",
			"status":    "pending",
			"domains":   append([]string(nil), domains...),
			"cache_dir": filepath.Clean(autoCertDir),
		}
	case strings.TrimSpace(certFile) != "":
		info, err := LoadCertificateInfo(certFile, now)
		if err == nil {
			out := CertificateInfoMap(info)
			out["path"] = filepath.Clean(certFile)
			return out
		}
		return map[string]any{
			"source": "file",
			"status": "down",
			"path":   filepath.Clean(certFile),
			"error":  err.Error(),
		}
	default:
		return map[string]any{
			"status": "disabled",
		}
	}
}
