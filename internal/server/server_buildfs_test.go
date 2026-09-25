package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/config"
	"github.com/kervanserver/kervan/internal/vfs"
)

func buildMemoryApp(t *testing.T) *App {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Server.DataDir = t.TempDir()
	cfg.Storage.DefaultBackend = "mem"
	cfg.Storage.Backends = map[string]config.BackendConfig{
		"mem": {Type: "memory"},
	}
	return &App{cfg: cfg}
}

func buildLocalApp(t *testing.T) *App {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Server.DataDir = t.TempDir()
	root := filepath.Join(t.TempDir(), "files")
	cfg.Storage.DefaultBackend = "local"
	cfg.Storage.Backends = map[string]config.BackendConfig{
		"local": {Type: "local", Options: map[string]string{"root": root}},
	}
	return &App{cfg: cfg}
}

func writeViaFS(t *testing.T, fs vfs.FileSystem, path, content string) {
	t.Helper()
	f, err := fs.Open(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	if _, err := f.Write([]byte(content)); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

func statViaFS(t *testing.T, fs vfs.FileSystem, path string) (int64, error) {
	t.Helper()
	info, err := fs.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// Backend type memory must hand every buildUserFS call for the same user the
// same in-memory world: the fsBuilder runs once per protocol connection and
// once per API request, so per-call instances would isolate every session's
// files from every other's.
func TestBuildUserFSMemoryBackendSharedPerUser(t *testing.T) {
	app := buildMemoryApp(t)
	user := &auth.User{Username: "alice", HomeDir: "/", Type: auth.UserTypeVirtual, Permissions: auth.DefaultUserPermissions(), Enabled: true}

	fs1, err := app.buildUserFS(user)
	if err != nil {
		t.Fatalf("first buildUserFS: %v", err)
	}
	writeViaFS(t, fs1, "/f.txt", "shared")

	fs2, err := app.buildUserFS(user)
	if err != nil {
		t.Fatalf("second buildUserFS: %v", err)
	}
	size, err := statViaFS(t, fs2, "/f.txt")
	if err != nil {
		t.Fatalf("file written via the first instance is invisible via the second: %v", err)
	}
	if size != 6 {
		t.Fatalf("size = %d, want 6", size)
	}
}

// Control: the local backend composes across calls (on-disk root).
func TestBuildUserFSLocalBackendComposes(t *testing.T) {
	app := buildLocalApp(t)
	user := &auth.User{Username: "alice", HomeDir: "/", Type: auth.UserTypeVirtual, Permissions: auth.DefaultUserPermissions(), Enabled: true}

	fs1, err := app.buildUserFS(user)
	if err != nil {
		t.Fatalf("first buildUserFS: %v", err)
	}
	writeViaFS(t, fs1, "/f.txt", "shared")

	fs2, err := app.buildUserFS(user)
	if err != nil {
		t.Fatalf("second buildUserFS: %v", err)
	}
	size, err := statViaFS(t, fs2, "/f.txt")
	if err != nil {
		t.Fatalf("local backend should compose across calls: %v", err)
	}
	if size != 6 {
		t.Fatalf("size = %d, want 6", size)
	}
}
