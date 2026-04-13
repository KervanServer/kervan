package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNPMCommandMatchesPlatform(t *testing.T) {
	got := npmCommand()
	if runtime.GOOS == "windows" && got != "npm.cmd" {
		t.Fatalf("expected npm.cmd on windows, got %q", got)
	}
	if runtime.GOOS != "windows" && got != "npm" {
		t.Fatalf("expected npm on non-windows, got %q", got)
	}
}

func TestCopyFileAndCopyDir(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "src")
	dstDir := filepath.Join(root, "dst")
	if err := os.MkdirAll(filepath.Join(srcDir, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir source tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "index.html"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "nested", "app.js"), []byte("console.log('ok')"), 0o600); err != nil {
		t.Fatalf("write nested source file: %v", err)
	}

	if err := copyDir(srcDir, dstDir); err != nil {
		t.Fatalf("copy dir: %v", err)
	}

	for _, rel := range []string{"index.html", filepath.Join("nested", "app.js")} {
		raw, err := os.ReadFile(filepath.Join(dstDir, rel))
		if err != nil {
			t.Fatalf("read copied file %s: %v", rel, err)
		}
		if len(raw) == 0 {
			t.Fatalf("expected copied file %s to be non-empty", rel)
		}
	}
}

func TestCopyDirAndCopyFileErrorPaths(t *testing.T) {
	root := t.TempDir()

	if err := copyDir(filepath.Join(root, "missing"), filepath.Join(root, "dst")); err == nil {
		t.Fatal("expected copyDir to fail for missing source")
	}

	srcFile := filepath.Join(root, "source.txt")
	if err := os.WriteFile(srcFile, []byte("data"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	if err := copyFile(srcFile, filepath.Join(root, "missing-dir", "dest.txt")); err == nil {
		t.Fatal("expected copyFile to fail when destination directory does not exist")
	}
}

func TestRunCommandRejectsUnsupportedCommand(t *testing.T) {
	err := runCommand(t.TempDir(), "definitely-missing-command-binary")
	if err == nil {
		t.Fatal("expected runCommand to reject unsupported command")
	}
	if !strings.Contains(err.Error(), "unsupported command") {
		t.Fatalf("expected unsupported command error, got %v", err)
	}
}

func TestRunCommandPropagatesExitFailure(t *testing.T) {
	dir := t.TempDir()
	scriptName := "npm"
	if runtime.GOOS == "windows" {
		scriptName = "npm.cmd"
	}
	script := filepath.Join(dir, scriptName)
	command := npmCommand()
	args := []string{"run", "build"}
	if runtime.GOOS == "windows" {
		if err := os.WriteFile(script, []byte("@echo off\r\nexit /b 7\r\n"), 0o644); err != nil {
			t.Fatalf("write windows script: %v", err)
		}
	} else {
		if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 7\n"), 0o755); err != nil {
			t.Fatalf("write shell script: %v", err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := runCommand(dir, command, args...)
	if err == nil {
		t.Fatal("expected runCommand to propagate non-zero exit")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected exit error, got %T", err)
	}
}
