package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func main() {
	rootDir, err := os.Getwd()
	if err != nil {
		exitf("get working directory: %v", err)
	}

	webuiDir := filepath.Join(rootDir, "webui")
	sourceDistDir := filepath.Join(webuiDir, "dist")
	embeddedDistDir := filepath.Join(rootDir, "internal", "webui", "dist")

	if err := runCommand(webuiDir, npmCommand(), "ci"); err != nil {
		exitf("npm ci: %v", err)
	}
	if err := runCommand(webuiDir, npmCommand(), "run", "build"); err != nil {
		exitf("npm run build: %v", err)
	}

	if err := os.RemoveAll(embeddedDistDir); err != nil {
		exitf("remove embedded dist: %v", err)
	}
	if err := copyDir(sourceDistDir, embeddedDistDir); err != nil {
		exitf("copy dist: %v", err)
	}

	fmt.Println("WebUI build copied to internal/webui/dist")
}

func npmCommand() string {
	if runtime.GOOS == "windows" {
		return "npm.cmd"
	}
	return "npm"
}

func runCommand(dir, name string, args ...string) error {
	if name != "npm" && name != "npm.cmd" {
		return errors.New("unsupported command")
	}
	// #nosec G204 -- command name is restricted to trusted npm binaries above.
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run command %s %v in %s: %w", name, args, dir, err)
	}
	return nil
}

func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source directory %s: %w", src, err)
	}
	if err := os.MkdirAll(dst, info.Mode()); err != nil {
		return fmt.Errorf("create destination directory %s: %w", dst, err)
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("read source directory %s: %w", src, err)
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	// #nosec G304 -- src/dst are derived from recursive walk under project-owned directories.
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source file %s: %w", src, err)
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("stat source file %s: %w", src, err)
	}

	// #nosec G304 -- destination path is constrained to internal/webui/dist mirror output.
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
	if err != nil {
		return fmt.Errorf("open destination file %s: %w", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close destination file %s: %w", dst, err)
	}
	return nil
}

func exitf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
