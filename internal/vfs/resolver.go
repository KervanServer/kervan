package vfs

import (
	"path"
	"strings"
)

const maxPathDepth = 256

type Resolver struct{}

func NewResolver() *Resolver { return &Resolver{} }

func (r *Resolver) Resolve(virtualPath string) (string, error) {
	cleaned := path.Clean("/" + virtualPath)

	if strings.ContainsRune(cleaned, '\x00') {
		return "", ErrForbiddenPathChar
	}
	if !strings.HasPrefix(cleaned, "/") {
		return "", ErrPathEscape
	}

	depth := 0
	for _, part := range strings.Split(cleaned, "/") {
		if part != "" {
			depth++
		}
	}
	if depth > maxPathDepth {
		return "", ErrPathTooDeep
	}
	return cleaned, nil
}

func (r *Resolver) ResolvePair(from, to string) (string, string, error) {
	f, err := r.Resolve(from)
	if err != nil {
		return "", "", err
	}
	t, err := r.Resolve(to)
	if err != nil {
		return "", "", err
	}
	return f, t, nil
}
