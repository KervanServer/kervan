package main

import (
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
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, info.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
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
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func exitf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
