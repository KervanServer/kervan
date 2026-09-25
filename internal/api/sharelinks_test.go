package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/store"
	"github.com/kervanserver/kervan/internal/vfs"
)

func TestShareLinkRepositoryCreateGetReserveDownload(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	repo := newShareLinkRepository(st)
	link, err := repo.Create("alice", "/docs/report.pdf", 24*time.Hour, 3)
	if err != nil {
		t.Fatalf("create share link: %v", err)
	}
	if link == nil {
		t.Fatal("expected created link")
	}
	if !strings.HasPrefix(link.Token, "share_") {
		t.Fatalf("unexpected token: %s", link.Token)
	}
	if link.DownloadCount != 0 {
		t.Fatalf("expected initial download count 0, got %d", link.DownloadCount)
	}

	saved, err := repo.Get(link.Token)
	if err != nil {
		t.Fatalf("get share link: %v", err)
	}
	if saved.Path != "/docs/report.pdf" {
		t.Fatalf("unexpected stored path: %s", saved.Path)
	}

	reserved, err := repo.ReserveDownload(link.Token, time.Now().UTC())
	if err != nil {
		t.Fatalf("reserve download: %v", err)
	}
	if reserved.DownloadCount != 1 {
		t.Fatalf("expected reserved download count 1, got %d", reserved.DownloadCount)
	}
	updated, err := repo.Get(link.Token)
	if err != nil {
		t.Fatalf("get share link after reservation: %v", err)
	}
	if updated.DownloadCount != 1 {
		t.Fatalf("expected persisted download count 1, got %d", updated.DownloadCount)
	}

	list, err := repo.ListByUsername("alice")
	if err != nil {
		t.Fatalf("list links: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one link for alice, got %d", len(list))
	}
	if err := repo.Delete(link.Token); err != nil {
		t.Fatalf("delete link: %v", err)
	}
	if _, err := repo.Get(link.Token); err == nil {
		t.Fatal("expected deleted link to be missing")
	}
}

func TestParseTTL(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		fallback time.Duration
		want     time.Duration
		wantErr  bool
	}{
		{name: "empty uses fallback", raw: "", fallback: 24 * time.Hour, want: 24 * time.Hour},
		{name: "hours", raw: "12h", fallback: time.Hour, want: 12 * time.Hour},
		{name: "days", raw: "2d", fallback: time.Hour, want: 48 * time.Hour},
		{name: "invalid", raw: "abc", fallback: time.Hour, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTTL(tc.raw, tc.fallback)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("unexpected ttl: got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestShareLinkRepositoryReserveDownloadRespectsLimit(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	repo := newShareLinkRepository(st)
	link, err := repo.Create("alice", "/docs/report.pdf", 24*time.Hour, 1)
	if err != nil {
		t.Fatalf("create share link: %v", err)
	}

	reserved, err := repo.ReserveDownload(link.Token, time.Now().UTC())
	if err != nil {
		t.Fatalf("reserve download: %v", err)
	}
	if reserved.DownloadCount != 1 {
		t.Fatalf("expected reserved download count 1, got %d", reserved.DownloadCount)
	}
	if _, err := repo.ReserveDownload(link.Token, time.Now().UTC()); !errors.Is(err, ErrShareLinkDownloadLimitExceeded) {
		t.Fatalf("expected download limit error, got %v", err)
	}
	if err := repo.ReleaseDownload(link.Token); err != nil {
		t.Fatalf("release download: %v", err)
	}
	reloaded, err := repo.Get(link.Token)
	if err != nil {
		t.Fatalf("reload link after release: %v", err)
	}
	if reloaded.DownloadCount != 0 {
		t.Fatalf("expected released download count 0, got %d", reloaded.DownloadCount)
	}
}

func TestHandleShareDownloadRollsBackReservationOnFailure(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	repo := newShareLinkRepository(st)
	link, err := repo.Create("alice", "/missing.txt", 24*time.Hour, 1)
	if err != nil {
		t.Fatalf("create share link: %v", err)
	}

	srv := &Server{
		shareLinks: repo,
		fsBuilder: func(string) (vfs.FileSystem, error) {
			return memory.New(), nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/share/"+link.Token, nil)
	rec := httptest.NewRecorder()
	srv.handleShareDownload(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected missing shared file to return 404, got %d: %s", rec.Code, rec.Body.String())
	}
	reloaded, err := repo.Get(link.Token)
	if err != nil {
		t.Fatalf("reload share link: %v", err)
	}
	if reloaded.DownloadCount != 0 {
		t.Fatalf("expected failed download to roll back reservation, got count %d", reloaded.DownloadCount)
	}
}

func TestHandleShareDownloadEscapesAttachmentFilename(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	repo := newShareLinkRepository(st)
	filename := `/reports/quarter"1.txt`
	link, err := repo.Create("alice", filename, 24*time.Hour, 0)
	if err != nil {
		t.Fatalf("create share link: %v", err)
	}

	mem := memory.New()
	if err := mem.MkdirAll("/reports", 0o755); err != nil {
		t.Fatalf("create reports dir: %v", err)
	}
	f, err := mem.Open(filename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("create backing file: %v", err)
	}
	if _, err := f.Write([]byte("ok")); err != nil {
		t.Fatalf("write backing file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close backing file: %v", err)
	}

	srv := &Server{
		shareLinks: repo,
		fsBuilder: func(string) (vfs.FileSystem, error) {
			return mem, nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/share/"+link.Token, nil)
	rec := httptest.NewRecorder()
	srv.handleShareDownload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected successful share download, got %d: %s", rec.Code, rec.Body.String())
	}
	got := rec.Header().Get("Content-Disposition")
	if !strings.Contains(got, `filename="quarter\"1.txt"`) {
		t.Fatalf("expected escaped filename in content-disposition, got %q", got)
	}
}

// Contract: the share-link lifecycle composes across real handler invocations
// over real persistence — an authenticated create, unauthenticated downloads
// that count against max_downloads, limit enforcement, and counts that
// survive a fresh repository instance over the same store.

func TestShareLinkLifecycleAcrossHandlersAndStore(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	repo := newShareLinkRepository(st)

	mounts := vfs.NewMountTable()
	mounts.Mount("/", memory.New(), false)
	perms := &vfs.UserPermissions{Upload: true, Download: true, Delete: true, Rename: true, CreateDir: true, ListDir: true}
	fsys := vfs.NewUserVFS(mounts, perms, nil)
	f, err := fsys.Open("/report.txt", os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := f.Write([]byte("quarterly report")); err != nil {
		t.Fatalf("seed content: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close seed file: %v", err)
	}

	s := &Server{
		shareLinks:   repo,
		fsBuilder:    func(string) (vfs.FileSystem, error) { return fsys, nil },
		auditLogPath: filepath.Join(dir, "audit.jsonl"),
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/share?path=/report.txt&ttl=1h&max_downloads=2", nil)
	req.Header.Set("X-Auth-User", "alice")
	s.handleFilesShare(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %q, want 201", rec.Code, rec.Body.String())
	}
	var created struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.Token == "" {
		t.Fatalf("create response %q (err=%v)", rec.Body.String(), err)
	}

	download := func() (int, string) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/share/"+created.Token, nil)
		s.handleShareDownload(rec, req)
		return rec.Code, rec.Body.String()
	}
	for i := 1; i <= 2; i++ {
		code, body := download()
		if code != http.StatusOK || body != "quarterly report" {
			t.Fatalf("download %d = (%d, %q), want (200, seeded content)", i, code, body)
		}
	}
	if code, _ := download(); code != http.StatusGone {
		t.Fatalf("download 3 status = %d, want 410 after max_downloads=2", code)
	}

	fresh := newShareLinkRepository(st)
	if _, err := fresh.ReserveDownload(created.Token, time.Now().UTC()); err == nil {
		t.Fatalf("download count/limit did not persist across a fresh repository over the same store")
	}
}
