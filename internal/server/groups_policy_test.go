package server

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/quota"
	"github.com/kervanserver/kervan/internal/store"
)

func appWithGroups(t *testing.T) (*App, *auth.UserRepository) {
	t.Helper()
	app := buildMemoryApp(t)
	st, err := store.Open(app.cfg.Server.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	users := auth.NewUserRepository(st)
	app.groups = auth.NewGroupRepository(st, users)
	app.cfg.Quota.Enabled = true
	app.cfg.Quota.DefaultMaxStorage = 0
	return app, users
}

func TestBuildUserFSAppliesPrimaryGroupPolicy(t *testing.T) {
	app, _ := appWithGroups(t)
	if err := app.groups.Create(&auth.Group{
		Name:        "readers",
		Permissions: auth.UserPermissions{Download: true, ListDir: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.groups.Create(&auth.Group{
		Name:        "small",
		Permissions: auth.DefaultUserPermissions(),
		MaxStorage:  8,
	}); err != nil {
		t.Fatal(err)
	}

	reader := &auth.User{Username: "r", Type: auth.UserTypeVirtual, Permissions: auth.DefaultUserPermissions(), PrimaryGroup: "readers"}
	fsys, err := app.buildUserFS(reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fsys.Open("/x.txt", os.O_CREATE|os.O_WRONLY, 0o644); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("group template must deny upload, got %v", err)
	}

	// custom_permissions restores the user's own permissions.
	reader.CustomPermissions = true
	fsys, err = app.buildUserFS(reader)
	if err != nil {
		t.Fatal(err)
	}
	writeViaFS(t, fsys, "/x.txt", "ok")

	// The group quota applies to members.
	small := &auth.User{Username: "s", Type: auth.UserTypeVirtual, Permissions: auth.DefaultUserPermissions(), PrimaryGroup: "small"}
	fsys, err = app.buildUserFS(small)
	if err != nil {
		t.Fatal(err)
	}
	f, err := fsys.Open("/big.txt", os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, werr := f.Write([]byte(strings.Repeat("x", 16)))
	_ = f.Close()
	if !errors.Is(werr, quota.ErrStorageExceeded) {
		t.Fatalf("group quota of 8 bytes not enforced: %v", werr)
	}

	// A per-user quota overrides the group's.
	small.Username, small.MaxStorage = "s2", -1
	fsys, err = app.buildUserFS(small)
	if err != nil {
		t.Fatal(err)
	}
	writeViaFS(t, fsys, "/big.txt", strings.Repeat("x", 16))
}
