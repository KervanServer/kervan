package server

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/quota"
	"github.com/kervanserver/kervan/internal/store"
	"github.com/kervanserver/kervan/internal/throttle"
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

func TestBuildUserFSAppliesSharedBandwidthLimit(t *testing.T) {
	app, _ := appWithGroups(t)
	app.userRates = throttle.NewRegistry()
	app.totalRate = throttle.NewAdjustable(0)
	app.cfg.Quota.Enabled = false
	const rate = 1 << 20 // 1 MiB/s
	if err := app.groups.Create(&auth.Group{Name: "slow", Permissions: auth.DefaultUserPermissions(), MaxBandwidth: rate}); err != nil {
		t.Fatal(err)
	}
	user := &auth.User{Username: "s", Type: auth.UserTypeVirtual, Permissions: auth.DefaultUserPermissions(), PrimaryGroup: "slow"}

	// Two "connections" of the same user share one limiter: together they
	// cannot exceed the user's rate.
	payload := make([]byte, 1<<20)
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		fsys, err := app.buildUserFS(user)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			f, err := fsys.Open("/f"+strconv.Itoa(i), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				t.Error(err)
				return
			}
			defer f.Close()
			if _, err := f.Write(payload); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	// 2 MiB at 1 MiB/s, minus the 256 KiB burst.
	if elapsed := time.Since(start); elapsed < 1500*time.Millisecond {
		t.Fatalf("2 MiB over two connections took %v; the 1 MiB/s user limit was not shared", elapsed)
	}

	// A per-user override of -1 lifts the limit.
	user.Username, user.MaxBandwidth = "fast", -1
	fsys, _ := app.buildUserFS(user)
	start = time.Now()
	writeViaFS(t, fsys, "/big", string(make([]byte, 4<<20)))
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("unlimited user throttled: %v", elapsed)
	}
}
