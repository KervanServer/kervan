package crypto

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
)

func BuildServerTLSConfig(minVersion, maxVersion, certFile, keyFile string) (*tls.Config, error) {
	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("cert_file and key_file are required")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS key pair (%s, %s): %w", certFile, keyFile, err)
	}
	min, err := ParseTLSVersion(minVersion)
	if err != nil {
		return nil, fmt.Errorf("parse minimum TLS version %q: %w", minVersion, err)
	}
	max, err := ParseTLSVersion(maxVersion)
	if err != nil {
		return nil, fmt.Errorf("parse maximum TLS version %q: %w", maxVersion, err)
	}
	if min > max {
		return nil, fmt.Errorf("min tls version cannot be higher than max tls version")
	}

	return BuildServerTLSConfigFromSource(min, max, func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		return &cert, nil
	}, []tls.Certificate{cert})
}

func BuildServerTLSConfigFromSource(
	minVersion uint16,
	maxVersion uint16,
	getCertificate func(*tls.ClientHelloInfo) (*tls.Certificate, error),
	certificates []tls.Certificate,
) (*tls.Config, error) {
	if minVersion < tls.VersionTLS12 {
		minVersion = tls.VersionTLS12
	}
	if maxVersion < minVersion {
		maxVersion = minVersion
	}
	return &tls.Config{
		// #nosec G402 -- minimum is clamped to TLS1.2 above.
		MinVersion:     minVersion,
		MaxVersion:     maxVersion,
		GetCertificate: getCertificate,
		Certificates:   certificates,
	}, nil
}

func ParseTLSVersion(v string) (uint16, error) {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "", "1.2", "tls1.2":
		return tls.VersionTLS12, nil
	case "1.3", "tls1.3":
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("unsupported tls version: %s", v)
	}
}

// WithClientAuth returns a copy of base that applies ftps.client_auth:
// "none" (default), "request" (verify a client certificate when one is
// presented) or "require" (reject clients without a valid certificate).
// Certificates are verified against the PEM bundle in caFile.
func WithClientAuth(base *tls.Config, mode, caFile string) (*tls.Config, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if base == nil || mode == "" || mode == "none" {
		return base, nil
	}
	var authType tls.ClientAuthType
	switch mode {
	case "request":
		authType = tls.VerifyClientCertIfGiven
	case "require":
		authType = tls.RequireAndVerifyClientCert
	default:
		return nil, fmt.Errorf("unsupported client_auth mode %q (none|request|require)", mode)
	}
	if strings.TrimSpace(caFile) == "" {
		return nil, fmt.Errorf("client_auth %q requires client_ca_file", mode)
	}
	pemBytes, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read client_ca_file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("client_ca_file %s contains no PEM certificates", caFile)
	}
	out := base.Clone()
	out.ClientAuth = authType
	out.ClientCAs = pool
	return out, nil
}
