//go:build !(linux || darwin || freebsd || windows)

package local

import "github.com/kervanserver/kervan/internal/vfs"

func diskStats(string) (*vfs.StatVFS, error) { return &vfs.StatVFS{}, nil }
