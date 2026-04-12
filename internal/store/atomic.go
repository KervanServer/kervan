package store

import (
	"os"
	"path/filepath"
)

func writeFileAtomically(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = replaceFile(tmpName, path); err != nil {
		return err
	}
	if err = syncDir(dir); err != nil {
		return err
	}
	return nil
}

func WriteFileAtomically(path string, data []byte, perm os.FileMode) error {
	return writeFileAtomically(path, data, perm)
}

func ReplaceTempFileAtomically(tmpPath, targetPath string) error {
	if err := replaceFile(tmpPath, targetPath); err != nil {
		return err
	}
	return syncDir(filepath.Dir(targetPath))
}

func syncDir(path string) error {
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
