package auth

import (
	"bytes"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// maxAuthorizedKeys bounds a user's key list.
const maxAuthorizedKeys = 50

// NormalizeAuthorizedKeys validates OpenSSH authorized_keys entries and
// returns them as "type base64 [comment]" lines, dropping duplicates of the
// same key. Options (from=..., no-pty, ...) are not supported by Kervan and
// are rejected rather than silently ignored.
func NormalizeAuthorizedKeys(entries []string) ([]string, error) {
	out := make([]string, 0, len(entries))
	var seen [][]byte
	for i, raw := range entries {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, comment, options, rest, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("key %d: not a valid OpenSSH public key", i+1)
		}
		if len(bytes.TrimSpace(rest)) > 0 {
			return nil, fmt.Errorf("key %d: one key per entry", i+1)
		}
		if len(options) > 0 {
			return nil, fmt.Errorf("key %d: authorized_keys options (%s) are not supported", i+1, strings.Join(options, ","))
		}
		wire := key.Marshal()
		duplicate := false
		for _, other := range seen {
			if bytes.Equal(other, wire) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		seen = append(seen, wire)
		normalized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		if comment = strings.TrimSpace(comment); comment != "" {
			normalized += " " + comment
		}
		out = append(out, normalized)
	}
	if len(out) > maxAuthorizedKeys {
		return nil, fmt.Errorf("at most %d keys per user", maxAuthorizedKeys)
	}
	return out, nil
}

// KeyInfo describes a stored authorized key for display.
type KeyInfo struct {
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
	Comment     string `json:"comment,omitempty"`
	Line        string `json:"line"`
}

// DescribeAuthorizedKeys summarizes stored keys; unparsable entries are
// skipped.
func DescribeAuthorizedKeys(entries []string) []KeyInfo {
	out := make([]KeyInfo, 0, len(entries))
	for _, line := range entries {
		key, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			continue
		}
		out = append(out, KeyInfo{Type: key.Type(), Fingerprint: ssh.FingerprintSHA256(key), Comment: comment, Line: line})
	}
	return out
}
