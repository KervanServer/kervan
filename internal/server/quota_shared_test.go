package server

import (
	"errors"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/quota"
	"github.com/kervanserver/kervan/internal/vfs"
)

// appWithSharedQuota returns an App whose users get a quota of maxStorage
// bytes from their primary group, with the production quota registry.
func appWithSharedQuota(t *testing.T, maxStorage int64) (*App, *auth.User) {
	t.Helper()
	app, _ := appWithGroups(t)
	app.quotas = quota.NewRegistry()
	if err := app.groups.Create(&auth.Group{
		Name:        "capped",
		Permissions: auth.DefaultUserPermissions(),
		MaxStorage:  maxStorage,
	}); err != nil {
		t.Fatal(err)
	}
	user := &auth.User{Username: "carol", HomeDir: "/", Type: auth.UserTypeVirtual, Permissions: auth.DefaultUserPermissions(), PrimaryGroup: "capped", Enabled: true}
	return app, user
}

// Two sessions of the same user (e.g. an FTP and an SFTP connection) are
// both open before either uploads. Each used to get its own tracker measured
// at 0 bytes, so together they could store twice the quota.
func TestBuildUserFSQuotaSharedAcrossSessions(t *testing.T) {
	app, user := appWithSharedQuota(t, 10)

	session1, err := app.buildUserFS(user)
	if err != nil {
		t.Fatal(err)
	}
	session2, err := app.buildUserFS(user)
	if err != nil {
		t.Fatal(err)
	}

	writeViaFS(t, session1, "/a.bin", "12345678") // 8 of 10 bytes

	f, err := session2.Open("/b.bin", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, werr := f.Write([]byte("12345678"))
	_ = f.Close()
	if !errors.Is(werr, quota.ErrStorageExceeded) {
		t.Fatalf("second session wrote past the shared 10-byte quota: err = %v", werr)
	}

	// Space freed by one session is available to the other.
	if err := session2.Remove("/a.bin"); err != nil {
		t.Fatal(err)
	}
	writeViaFS(t, session1, "/c.bin", "1234567890")
}

// Many sessions of one user upload at the same time: whatever the
// interleaving, the bytes accepted and stored never exceed the quota.
func TestBuildUserFSQuotaConcurrentSessionsCannotExceedQuota(t *testing.T) {
	const (
		limit    = 64
		sessions = 8
		chunk    = 4
		chunks   = 4 // 16 bytes per session, 128 requested in total
	)
	app, user := appWithSharedQuota(t, limit)

	fss := make([]vfs.FileSystem, sessions)
	for i := range fss {
		fsys, err := app.buildUserFS(user)
		if err != nil {
			t.Fatal(err)
		}
		fss[i] = fsys
	}

	var accepted atomic.Int64
	var refused atomic.Int64
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, fsys := range fss {
		wg.Add(1)
		go func(i int, fsys vfs.FileSystem) {
			defer wg.Done()
			<-start
			f, err := fsys.Open("/f"+strconv.Itoa(i), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				t.Error(err)
				return
			}
			defer f.Close()
			for range chunks {
				n, err := f.Write(make([]byte, chunk))
				accepted.Add(int64(n))
				if errors.Is(err, quota.ErrStorageExceeded) {
					refused.Add(1)
					return
				}
				if err != nil {
					t.Error(err)
					return
				}
			}
		}(i, fsys)
	}
	close(start)
	wg.Wait()

	if got := accepted.Load(); got != limit {
		t.Fatalf("sessions together wrote %d bytes, want exactly the %d-byte quota", got, limit)
	}
	if refused.Load() == 0 {
		t.Fatal("no session was refused although 128 bytes were requested against a 64-byte quota")
	}
	used, _, err := quota.MeasureUsage(fss[0], "/")
	if err != nil {
		t.Fatal(err)
	}
	if used > limit {
		t.Fatalf("stored %d bytes, quota is %d", used, limit)
	}
}

// A changed quota reaches sessions that are already open, because they
// share the tracker the next sign-in updates.
func TestBuildUserFSQuotaLimitChangeReachesOpenSessions(t *testing.T) {
	app, user := appWithSharedQuota(t, 4)
	open, err := app.buildUserFS(user)
	if err != nil {
		t.Fatal(err)
	}

	user.MaxStorage = 16 // per-user override, as an admin would set it
	if _, err := app.buildUserFS(user); err != nil {
		t.Fatal(err)
	}
	writeViaFS(t, open, "/x.bin", "0123456789")
}
