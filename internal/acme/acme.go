package acme

import (
	"crypto/tls"
	"errors"
	"net/http"
	"os"
	"strings"

	xacme "golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

type Config struct {
	CacheDir string
	Email    string
	Domains  []string
}

type Manager struct {
	manager *autocert.Manager
}

func New(cfg Config) (*Manager, error) {
	if strings.TrimSpace(cfg.CacheDir) == "" {
		return nil, errors.New("acme cache dir is required")
	}
	if len(cfg.Domains) == 0 {
		return nil, errors.New("acme domains are required")
	}
	if err := os.MkdirAll(cfg.CacheDir, 0o700); err != nil {
		return nil, err
	}
	domains := make([]string, 0, len(cfg.Domains))
	for _, domain := range cfg.Domains {
		trimmed := strings.TrimSpace(domain)
		if trimmed != "" {
			domains = append(domains, trimmed)
		}
	}
	if len(domains) == 0 {
		return nil, errors.New("acme domains are required")
	}
	return &Manager{
		manager: &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			Cache:      autocert.DirCache(cfg.CacheDir),
			Email:      strings.TrimSpace(cfg.Email),
			HostPolicy: autocert.HostWhitelist(domains...),
		},
	}, nil
}

func (m *Manager) HTTPHandler(next http.Handler) http.Handler {
	if m == nil || m.manager == nil {
		if next == nil {
			return http.NotFoundHandler()
		}
		return next
	}
	if next == nil {
		next = http.NotFoundHandler()
	}
	return m.manager.HTTPHandler(next)
}

func (m *Manager) TLSConfig(minVersion, maxVersion uint16) *tls.Config {
	if minVersion < tls.VersionTLS12 {
		minVersion = tls.VersionTLS12
	}
	if maxVersion < minVersion {
		maxVersion = minVersion
	}
	cfg := &tls.Config{
		// #nosec G402 -- minimum is clamped to TLS1.2 above.
		MinVersion:     minVersion,
		MaxVersion:     maxVersion,
		GetCertificate: m.manager.GetCertificate,
		NextProtos: []string{
			"h2",
			"http/1.1",
			xacme.ALPNProto,
		},
	}
	return cfg
}
