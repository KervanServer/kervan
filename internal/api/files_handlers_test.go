package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kervanserver/kervan/internal/storage/local"
	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/vfs"
)

// Contract: a directory is not downloadable. Declaring a directory's size as
// Content-Length and then failing to read it answers 200 with a body shorter
// than promised, truncating the response for every client. handleShareDownload
// and handleFilesShare already refuse the same target.
func TestFileDownloadRefusesDirectory(t *testing.T) {
	// The local backend is required: it is the default backend and the only
	// one whose Open succeeds for a directory (memory refuses with an error).
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	backend, err := local.New(local.Options{Root: root, CreateRoot: true})
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	mounts := vfs.NewMountTable()
	mounts.Mount("/", backend, false)
	fsys := vfs.NewUserVFS(mounts, &vfs.UserPermissions{
		Upload: true, Download: true, Delete: true, Rename: true, CreateDir: true, ListDir: true,
	}, nil)
	s := &Server{
		fsBuilder:    func(string) (vfs.FileSystem, error) { return fsys, nil },
		auditLogPath: "",
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/files/download?path=/subdir", nil)
	req.Header.Set("X-Auth-User", "alice")
	s.handleFilesDownload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("directory download status = %d, want 400 (body %q)", rec.Code, rec.Body.String())
	}
	// A refused download must not advertise a length it will not deliver.
	if cl := rec.Header().Get("Content-Length"); cl != "" {
		t.Fatalf("refused directory download still declared Content-Length %q", cl)
	}
}

// TestFileDownloadDeclaredLengthMatchesBody pins the HTTP invariant the guard
// protects: a declared Content-Length always matches the delivered body.
func TestFileDownloadDeclaredLengthMatchesBody(t *testing.T) {
	root := t.TempDir()
	payload := []byte("exact length payload\n")
	if err := os.WriteFile(filepath.Join(root, "file.bin"), payload, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	backend, err := local.New(local.Options{Root: root, CreateRoot: true})
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	mounts := vfs.NewMountTable()
	mounts.Mount("/", backend, false)
	fsys := vfs.NewUserVFS(mounts, &vfs.UserPermissions{
		Upload: true, Download: true, Delete: true, Rename: true, CreateDir: true, ListDir: true,
	}, nil)
	s := &Server{
		fsBuilder:    func(string) (vfs.FileSystem, error) { return fsys, nil },
		auditLogPath: "",
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/files/download?path=/file.bin", nil)
	req.Header.Set("X-Auth-User", "alice")
	s.handleFilesDownload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	declared := rec.Header().Get("Content-Length")
	if declared != strconv.Itoa(len(payload)) {
		t.Fatalf("Content-Length = %q, want %d", declared, len(payload))
	}
	if got := rec.Body.String(); got != string(payload) {
		t.Fatalf("body = %q, want %q", got, payload)
	}
}

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

// truncatedUploadBody yields a short prefix and then fails, exactly like a
// client that hangs up partway through an upload.
type truncatedUploadBody struct{ sent bool }

func (b *truncatedUploadBody) Read(p []byte) (int, error) {
	if !b.sent {
		b.sent = true
		return copy(p, []byte("partial")), nil
	}
	return 0, io.ErrUnexpectedEOF
}

// TestFileUploadFailureLeavesExistingFileIntact pins that a rejected upload
// does not destroy the file it was overwriting. handleFilesUpload used to open
// the target with O_CREATE|O_WRONLY|O_TRUNC and only then stream the body — and
// the backend applies O_TRUNC at Open time — so an upload that failed partway
// left the previous contents gone and only a partial prefix behind, despite the
// handler answering 500 and accepting nothing.
func TestFileUploadFailureLeavesExistingFileIntact(t *testing.T) {
	backend := memory.New()
	mounts := vfs.NewMountTable()
	mounts.Mount("/", backend, false)
	fsys := vfs.NewUserVFS(mounts, &vfs.UserPermissions{
		Upload: true, Download: true, Delete: true,
		Rename: true, CreateDir: true, ListDir: true,
	}, nil)
	s := &Server{fsBuilder: func(string) (vfs.FileSystem, error) { return fsys, nil }}

	const original = "ORIGINAL CONTENT THAT MUST SURVIVE A FAILED UPLOAD"
	w, err := fsys.Open("/f.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := w.Write([]byte(original)); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("seed close: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/files/upload?path=/f.txt", &truncatedUploadBody{})
	req.Header.Set("X-Auth-User", "alice")
	rec := httptest.NewRecorder()
	s.handleFilesUpload(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("precondition: a failing body must report 500, got %d", rec.Code)
	}

	r, err := fsys.Open("/f.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("reopen after failed upload: %v", err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != original {
		t.Fatalf("a rejected upload destroyed the existing file: before %q, after %q", original, string(got))
	}
}

// TestFileUploadSuccessLeavesNoTempFile is the control for the temp-file
// staging: a complete upload must still replace the target and must not leave
// the staging file behind for a client to see.
func TestFileUploadSuccessLeavesNoTempFile(t *testing.T) {
	backend := memory.New()
	mounts := vfs.NewMountTable()
	mounts.Mount("/", backend, false)
	fsys := vfs.NewUserVFS(mounts, &vfs.UserPermissions{
		Upload: true, Download: true, Delete: true,
		Rename: true, CreateDir: true, ListDir: true,
	}, nil)
	s := &Server{fsBuilder: func(string) (vfs.FileSystem, error) { return fsys, nil }}

	req := httptest.NewRequest(http.MethodPost, "/api/files/upload?path=/f.txt", strings.NewReader("NEWCONTENT"))
	req.Header.Set("X-Auth-User", "alice")
	rec := httptest.NewRecorder()
	s.handleFilesUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("control: successful upload got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	r, err := fsys.Open("/f.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer r.Close()
	got, _ := io.ReadAll(r)
	if string(got) != "NEWCONTENT" {
		t.Fatalf("control: successful upload left %q, want %q", string(got), "NEWCONTENT")
	}
	if _, err := fsys.Stat("/f.txt" + uploadTempSuffix); err == nil {
		t.Fatalf("control: staging file %q survived a successful upload", "/f.txt"+uploadTempSuffix)
	}
}
