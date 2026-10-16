package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kervanserver/kervan/internal/storage/memory"
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
