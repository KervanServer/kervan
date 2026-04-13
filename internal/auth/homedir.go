package auth

import (
	"errors"
	"path"
	"strings"
)

// NormalizeHomeDir normalizes a user home directory into a canonical
// virtual path (for example "/alice/docs") and rejects traversal attempts.
func NormalizeHomeDir(raw string) (string, error) {
	home := strings.TrimSpace(raw)
	if home == "" {
		return "/", nil
	}
	if strings.ContainsRune(home, rune(0)) {
		return "", errors.New("home directory contains invalid characters")
	}
	home = strings.ReplaceAll(home, `\`, "/")
	if !strings.HasPrefix(home, "/") {
		home = "/" + home
	}
	for _, segment := range strings.Split(home, "/") {
		if strings.TrimSpace(segment) == ".." {
			return "", errors.New("home directory traversal is not allowed")
		}
	}
	clean := path.Clean(home)
	if clean == "." || clean == "/" {
		return "/", nil
	}
	if !strings.HasPrefix(clean, "/") {
		clean = "/" + clean
	}
	return clean, nil
}
