package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/vfs"
)

// Contract: the API file handlers compose over a real user VFS —
// handleFilesUpload persists the raw request body at the requested virtual
// path and handleFilesDownload streams the same bytes back.

func TestFileUploadDownloadIntegration(t *testing.T) {
	backend := memory.New()
	mounts := vfs.NewMountTable()
	mounts.Mount("/", backend, false)
	fsys := vfs.NewUserVFS(mounts, &vfs.UserPermissions{
		Upload: true, Download: true, Delete: true, Rename: true, CreateDir: true, ListDir: true,
	}, nil)
	s := &Server{
		fsBuilder:    func(string) (vfs.FileSystem, error) { return fsys, nil },
		auditLogPath: "",
	}

	payload := "api file upload integration payload\r\nline two\n"

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/files/upload", strings.NewReader(payload))
	req.Header.Set("X-Auth-User", "alice")
	s.handleFilesUpload(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing-path upload status = %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/files/upload?path=/upload.txt", strings.NewReader(payload))
	req.Header.Set("X-Auth-User", "alice")
	s.handleFilesUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body = %q, want 200", rec.Code, rec.Body.String())
	}

	f, err := backend.Open("/upload.txt", 0, 0)
	if err != nil {
		t.Fatalf("uploaded file missing from the shared backend: %v", err)
	}
	got, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("uploaded content mismatch: got %q, want %q", got, payload)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/files/download?path=/upload.txt", nil)
	req.Header.Set("X-Auth-User", "alice")
	s.handleFilesDownload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("download status = %d, want 200", rec.Code)
	}
	downloaded, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read download body: %v", err)
	}
	if string(downloaded) != payload {
		t.Fatalf("download content mismatch: got %q, want %q", downloaded, payload)
	}
}
