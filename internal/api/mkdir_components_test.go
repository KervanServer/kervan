package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/kervanserver/kervan/internal/storage/local"
	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/vfs"
)

func TestFileMkdirRejectsRegularFileComponents(t *testing.T) {
	for _, kind := range []string{"memory", "local_control"} {
		t.Run(kind, func(t *testing.T) {
			for _, target := range []string{"/blocker", "/blocker/child", "/blocker/child/deep"} {
				t.Run(target, func(t *testing.T) {
					var backend vfs.FileSystem = memory.New()
					if kind == "local_control" {
						b, err := local.New(local.Options{Root: t.TempDir()})
						if err != nil {
							t.Fatal(err)
						}
						backend = b
					}
					f, err := backend.Open("/blocker", os.O_CREATE|os.O_WRONLY, 0o644)
					if err != nil {
						t.Fatal(err)
					}
					const original = "regular file, not a directory"
					if _, err := f.Write([]byte(original)); err != nil {
						t.Fatal(err)
					}
					if err := f.Close(); err != nil {
						t.Fatal(err)
					}
					mounts := vfs.NewMountTable()
					mounts.Mount("/", backend, false)
					fsys := vfs.NewUserVFS(mounts, &vfs.UserPermissions{Upload: true, Download: true, CreateDir: true, ListDir: true}, nil)
					s := &Server{fsBuilder: func(string) (vfs.FileSystem, error) { return fsys, nil }}
					mkdir := func(p string) *httptest.ResponseRecorder {
						req := httptest.NewRequest(http.MethodPost, "/api/files/mkdir", strings.NewReader(`{"path":"`+p+`"}`))
						req.Header.Set("X-Auth-User", "alice")
						rec := httptest.NewRecorder()
						s.handleFilesMkdir(rec, req)
						return rec
					}
					// Controls: root, new directories, and repeated existing directories succeed.
					for _, p := range []string{"/", "/valid/nested", "/valid/nested"} {
						if rec := mkdir(p); rec.Code != http.StatusCreated {
							t.Fatalf("control: mkdir %s = %d: %s", p, rec.Code, rec.Body.String())
						}
					}
					if info, err := backend.Stat("/valid/nested"); err != nil || !info.IsDir() {
						t.Fatalf("control: valid directory missing: %v, %v", info, err)
					}
					rec := mkdir(target)
					if rec.Code != http.StatusBadRequest {
						t.Errorf("FAIL: mkdir %s returned %d, want 400 for regular-file component; body=%s", target, rec.Code, rec.Body.String())
					}
					for _, p := range []string{"/blocker/child", "/blocker/child/deep"} {
						if _, err := backend.Stat(p); err == nil {
							t.Errorf("FAIL: mkdir created impossible descendant %s beneath a regular file", p)
						}
					}
					r, err := backend.Open("/blocker", os.O_RDONLY, 0)
					if err != nil {
						t.Fatal(err)
					}
					got, readErr := io.ReadAll(r)
					closeErr := r.Close()
					if readErr != nil || closeErr != nil || string(got) != original {
						t.Errorf("file changed: content=%q read=%v close=%v", got, readErr, closeErr)
					}
				})
			}
		})
	}
}
