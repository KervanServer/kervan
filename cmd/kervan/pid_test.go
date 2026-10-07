package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestWritePIDFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", "kervan.pid")
	if err := writePIDFile(path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.TrimSpace(string(raw)) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("pid file content %q", raw)
	}
	// Our own PID in the file is not a conflict (restart in place).
	if err := writePIDFile(path); err != nil {
		t.Fatal(err)
	}
	// A stale PID from a dead process is overwritten.
	_ = os.WriteFile(path, []byte("999999999\n"), 0o644)
	if err := writePIDFile(path); err != nil {
		t.Fatalf("stale pid not replaced: %v", err)
	}
	// The PID of another live process (our parent) is refused.
	_ = os.WriteFile(path, []byte(strconv.Itoa(os.Getppid())), 0o644)
	if err := writePIDFile(path); err == nil {
		t.Fatal("live foreign pid overwritten")
	}
}
