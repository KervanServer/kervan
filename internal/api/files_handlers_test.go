package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/vfs"
)

func TestFileHandlersRequirePath(t *testing.T) {
	mem := memory.New()
	srv := &Server{
		fsBuilder: func(string) (vfs.FileSystem, error) {
			return mem, nil
		},
	}

	tests := []struct {
		name    string
		method  string
		target  string
		body    []byte
		handler func(http.ResponseWriter, *http.Request)
	}{
		{name: "upload", method: http.MethodPost, target: "/api/files/upload", body: []byte("payload"), handler: srv.handleFilesUpload},
		{name: "download", method: http.MethodGet, target: "/api/files/download", handler: srv.handleFilesDownload},
		{name: "stat", method: http.MethodGet, target: "/api/files/stat", handler: srv.handleFilesStat},
		{name: "delete", method: http.MethodDelete, target: "/api/files/delete", handler: srv.handleFilesDelete},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.target, bytes.NewReader(tc.body))
			req.Header.Set("X-Auth-User", "alice")
			rec := httptest.NewRecorder()

			tc.handler(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
			var payload map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if payload["error"] != "path is required" {
				t.Fatalf("expected path required error, got %#v", payload)
			}
		})
	}
}

func TestHandleFilesRenameRequiresFromAndTo(t *testing.T) {
	mem := memory.New()
	srv := &Server{
		fsBuilder: func(string) (vfs.FileSystem, error) {
			return mem, nil
		},
	}

	tests := []struct {
		name   string
		target string
		body   []byte
	}{
		{name: "missing query and body", target: "/api/files/rename", body: []byte(`{}`)},
		{name: "missing to", target: "/api/files/rename?from=/a.txt", body: nil},
		{name: "missing from", target: "/api/files/rename?to=/b.txt", body: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.target, bytes.NewReader(tc.body))
			req.Header.Set("X-Auth-User", "alice")
			rec := httptest.NewRecorder()

			srv.handleFilesRename(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
			var payload map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if payload["error"] != "from and to are required" {
				t.Fatalf("unexpected payload: %#v", payload)
			}
		})
	}
}
