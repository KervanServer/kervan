package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kervanserver/kervan/internal/storage/local"
	"github.com/kervanserver/kervan/internal/vfs"
)

func TestFileUploadPreservesUnrelatedStagingSibling(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		name := "complete"
		if interrupted {
			name = "interrupted"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			const original = "unrelated user content"
			for _, file := range []string{"kervan-upload-f.txt", "neighbor.txt"} {
				if err := os.WriteFile(filepath.Join(root, file), []byte(original), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("old target"), 0o644); err != nil {
				t.Fatal(err)
			}
			backend, err := local.New(local.Options{Root: root})
			if err != nil {
				t.Fatal(err)
			}
			mounts := vfs.NewMountTable()
			mounts.Mount("/", backend, false)
			fsys := vfs.NewUserVFS(mounts, &vfs.UserPermissions{Upload: true, Download: true, Delete: true, Rename: true, ListDir: true}, nil)
			s := &Server{fsBuilder: func(string) (vfs.FileSystem, error) { return fsys, nil }}
			var body io.Reader = strings.NewReader("new target")
			wantStatus, wantTarget := http.StatusOK, "new target"
			if interrupted {
				body = &truncatedUploadBody{}
				wantStatus, wantTarget = http.StatusInternalServerError, "old target"
			}
			req := httptest.NewRequest(http.MethodPost, "/api/files/upload?path=/f.txt", body)
			req.Header.Set("X-Auth-User", "alice")
			rec := httptest.NewRecorder()
			s.handleFilesUpload(rec, req)
			if rec.Code != wantStatus {
				t.Fatalf("control: status = %d, want %d: %s", rec.Code, wantStatus, rec.Body.String())
			}
			for file, want := range map[string]string{"f.txt": wantTarget, "neighbor.txt": original, "kervan-upload-f.txt": original} {
				got, err := os.ReadFile(filepath.Join(root, file))
				if err != nil || string(got) != want {
					t.Errorf("FAIL: %s = %q, err=%v; want %q", file, got, err, want)
				}
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 3 {
				t.Errorf("staging artifacts remain: %v", entries)
			}
		})
	}
}
