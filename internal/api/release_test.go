package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/store"
	"github.com/kervanserver/kervan/internal/vfs"
)

type releaseCountingFS struct {
	vfs.FileSystem
	released *atomic.Int32
}

func (r releaseCountingFS) Release() { r.released.Add(1) }

// Every WebUI/API file request builds its own filesystem and must release
// it when the request ends, even when a handler leaves an upload unfinished
// (client disconnect, quota refusal): otherwise the user's shared quota
// tracker stays "busy" and is never re-measured.
func TestFileHandlersReleaseUserFS(t *testing.T) {
	var built, released atomic.Int32
	backend := memory.New()
	mounts := vfs.NewMountTable()
	mounts.Mount("/", backend, false)
	s := &Server{
		fsBuilder: func(string) (vfs.FileSystem, error) {
			built.Add(1)
			return releaseCountingFS{
				FileSystem: vfs.NewUserVFS(mounts, nil, nil),
				released:   &released,
			}, nil
		},
	}

	requests := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		target  string
		body    string
	}{
		{"upload", s.handleFilesUpload, http.MethodPost, "/api/files/upload?path=/a.txt", "hello"},
		{"list", s.handleFilesList, http.MethodGet, "/api/files/list?path=/", ""},
		{"mkdir", s.handleFilesMkdir, http.MethodPost, "/api/files/mkdir?path=/d", ""},
		{"download", s.handleFilesDownload, http.MethodGet, "/api/files/download?path=/a.txt", ""},
		{"rename", s.handleFilesRename, http.MethodPost, "/api/files/rename?from=/a.txt&to=/b.txt", ""},
		{"delete", s.handleFilesDelete, http.MethodDelete, "/api/files/delete?path=/d", ""},
	}
	for _, rq := range requests {
		before := built.Load()
		req := httptest.NewRequest(rq.method, rq.target, strings.NewReader(rq.body))
		req.Header.Set("X-Auth-User", "alice")
		rec := httptest.NewRecorder()
		rq.handler(rec, req)
		if built.Load() == before {
			t.Fatalf("%s: handler did not build a filesystem (status %d)", rq.name, rec.Code)
		}
		if got, want := released.Load(), built.Load(); got != want {
			t.Fatalf("%s: %d of %d request filesystems released (status %d, body %q)", rq.name, got, want, rec.Code, rec.Body.String())
		}
	}
}

// The public share-link endpoints build the link owner's filesystem per
// request, like the authenticated file handlers, and must release it on
// every outcome: a served download, a missing file, and the /info probe.
func TestShareLinkHandlersReleaseUserFS(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	repo := newShareLinkRepository(st)
	present, err := repo.Create("alice", "/report.txt", 24*time.Hour, 0, "")
	if err != nil {
		t.Fatalf("create share link: %v", err)
	}
	missing, err := repo.Create("alice", "/missing.txt", 24*time.Hour, 0, "")
	if err != nil {
		t.Fatalf("create share link: %v", err)
	}

	mem := memory.New()
	f, err := mem.Open("/report.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("create backing file: %v", err)
	}
	if _, err := f.Write([]byte("ok")); err != nil {
		t.Fatalf("write backing file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close backing file: %v", err)
	}

	var built, released atomic.Int32
	srv := &Server{
		shareLinks: repo,
		fsBuilder: func(string) (vfs.FileSystem, error) {
			built.Add(1)
			return releaseCountingFS{FileSystem: mem, released: &released}, nil
		},
	}

	cases := []struct {
		name   string
		target string
		status int
	}{
		{"download", "/api/v1/share/" + present.Token, http.StatusOK},
		{"missing file", "/api/v1/share/" + missing.Token, http.StatusNotFound},
		{"info", "/api/v1/share/" + present.Token + "/info", http.StatusOK},
	}
	for _, tc := range cases {
		before := built.Load()
		req := httptest.NewRequest(http.MethodGet, tc.target, nil)
		rec := httptest.NewRecorder()
		srv.handleShareDownload(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("%s: status %d, want %d (body %q)", tc.name, rec.Code, tc.status, rec.Body.String())
		}
		if built.Load() == before {
			t.Fatalf("%s: handler did not build the owner's filesystem", tc.name)
		}
		if got, want := released.Load(), built.Load(); got != want {
			t.Fatalf("%s: %d of %d share-link filesystems released", tc.name, got, want)
		}
	}
}
