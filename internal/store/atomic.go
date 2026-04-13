package store

import (
	"fmt"
	"os"
	"path/filepath"
)

func writeFileAtomically(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary file for %s: %w", path, err)
	}
	if err = tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set temporary file permissions for %s: %w", path, err)
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary file for %s: %w", path, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temporary file for %s: %w", path, err)
	}
	if err = replaceFile(tmpName, path); err != nil {
		return fmt.Errorf("replace %s with temporary file: %w", path, err)
	}
	if err = syncDir(dir); err != nil {
		return fmt.Errorf("sync directory %s: %w", dir, err)
	}
	return nil
}

func WriteFileAtomically(path string, data []byte, perm os.FileMode) error {
	return writeFileAtomically(path, data, perm)
}

func ReplaceTempFileAtomically(tmpPath, targetPath string) error {
	if err := replaceFile(tmpPath, targetPath); err != nil {
		return fmt.Errorf("replace %s with %s: %w", targetPath, tmpPath, err)
	}
	if err := syncDir(filepath.Dir(targetPath)); err != nil {
		return fmt.Errorf("sync directory for %s: %w", targetPath, err)
	}
	return nil
}

func syncDir(path string) error {
	// #nosec G304 -- path is derived from internal store file location.
	dir, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return nil
	}
	return nil
}
