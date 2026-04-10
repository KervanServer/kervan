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
		return nil, err
	}
	min, err := parseTLSVersion(minVersion)
	if err != nil {
		return nil, err
	}
	max, err := parseTLSVersion(maxVersion)
	if err != nil {
		return nil, err
	}
	if min > max {
		return nil, fmt.Errorf("min tls version cannot be higher than max tls version")
	}

	return &tls.Config{
		MinVersion:   min,
		MaxVersion:   max,
		Certificates: []tls.Certificate{cert},
	}, nil
}

func parseTLSVersion(v string) (uint16, error) {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "", "1.2", "tls1.2":
		return tls.VersionTLS12, nil
	case "1.3", "tls1.3":
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("unsupported tls version: %s", v)
	}
}
