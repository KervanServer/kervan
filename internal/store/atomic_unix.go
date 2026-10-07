//go:build !windows

package store

import (
	"os"
	"syscall"
)

func replaceFile(from, to string) error {
	return os.Rename(from, to)
}

// lockFile takes an exclusive advisory lock that serializes store writes
// across processes (the server and CLI commands share one data directory).
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
