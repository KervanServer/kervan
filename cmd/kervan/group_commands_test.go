package main

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kervanserver/kervan/internal/config"
)

func TestRunGroupCommands(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.Server.DataDir = dataDir
	})
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := runGroupCommand(&out, append(args, "--config", configPath))
		return out.String(), err
	}

	if _, err := run("create", "--name", "readers", "--permissions", "download,list_dir", "--max-storage", "1048576"); err != nil {
		t.Fatal(err)
	}
	if _, err := run("create", "--name", "x", "--permissions", "fly"); err == nil {
		t.Fatal("unknown permission accepted")
	}
	if err := runUserCreateCommand(io.Discard, []string{"--config", configPath, "--username", "ann", "--password", "StrongPass123!", "--group", "nope"}); err == nil {
		t.Fatal("user created into a missing group")
	}
	if err := runUserCreateCommand(io.Discard, []string{"--config", configPath, "--username", "ann", "--password", "StrongPass123!", "--group", "Readers"}); err != nil {
		t.Fatal(err)
	}

	out, err := run("list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "readers") || !strings.Contains(out, "download,list_dir") || !strings.Contains(out, "1048576") {
		t.Fatalf("group list output:\n%s", out)
	}
	if fields := strings.Fields(strings.Split(out, "\n")[1]); fields[1] != "1" {
		t.Fatalf("member count = %s, want 1", fields[1])
	}

	if _, err := run("delete", "--name", "readers"); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("delete with members: %v", err)
	}
	if _, err := run("delete", "--name", "readers", "--force"); err != nil {
		t.Fatal(err)
	}
}
