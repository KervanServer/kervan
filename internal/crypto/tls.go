package crypto

import (
	"crypto/tls"
	"fmt"
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
