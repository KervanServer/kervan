package memory_test

import (
	"errors"
	"io"
	"os"
	"testing"

	"github.com/kervanserver/kervan/internal/storage/local"
	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/vfs"
)

func TestBackendDirectoryRenameMovesSubtree(t *testing.T) {
	for _, kind := range []string{"memory", "local_control"} {
		t.Run(kind, func(t *testing.T) {
			var b vfs.FileSystem = memory.New()
			if kind == "local_control" {
				backend, err := local.New(local.Options{Root: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				b = backend
			}
			mounts := vfs.NewMountTable()
			mounts.Mount("/", b, false)
			fsys := vfs.NewUserVFS(mounts, &vfs.UserPermissions{Upload: true, Download: true, Rename: true, CreateDir: true, ListDir: true}, nil)
			for _, dir := range []string{"/src/nested/empty", "/src-other"} {
				if err := fsys.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for p, content := range map[string]string{"/src/top.txt": "top", "/src/nested/data.txt": "nested", "/src-other/control.txt": "untouched"} {
				f, err := fsys.Open(p, os.O_CREATE|os.O_WRONLY, 0o644)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.Write([]byte(content)); err != nil {
					t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			}
			// Control: ordinary file renames must remain functional.
			if err := fsys.Rename("/src/top.txt", "/src/renamed.txt"); err != nil {
				t.Fatal(err)
			}
			openChild, err := fsys.Open("/src/nested/data.txt", os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer openChild.Close()
			if err := fsys.Rename("/src", "/moved"); err != nil {
				t.Fatalf("rename: %v", err)
			}
			// Handles follow their node even when its containing directory moves.
			if _, err := openChild.WriteAt([]byte("!"), 6); err != nil {
				t.Fatal(err)
			}
			if err := openChild.Close(); err != nil {
				t.Fatal(err)
			}
			for p, want := range map[string]string{"/moved/renamed.txt": "top", "/moved/nested/data.txt": "nested!", "/src-other/control.txt": "untouched"} {
				f, err := fsys.Open(p, os.O_RDONLY, 0)
				if err != nil {
					t.Errorf("FAIL: moved subtree file %s unavailable: %v", p, err)
					continue
				}
				got, err := io.ReadAll(f)
				_ = f.Close()
				if err != nil || string(got) != want {
					t.Errorf("FAIL: %s = %q, err=%v, want %q", p, got, err, want)
				}
			}
			if info, err := fsys.Stat("/moved/nested/empty"); err != nil || !info.IsDir() {
				t.Errorf("FAIL: empty descendant missing: info=%v err=%v", info, err)
			}
			for _, p := range []string{"/src", "/src/renamed.txt", "/src/nested/data.txt", "/src/nested/empty"} {
				if _, err := fsys.Stat(p); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("FAIL: stale source %s still exists: %v", p, err)
				}
			}
			entries, err := fsys.ReadDir("/moved")
			if err != nil || len(entries) != 2 {
				t.Errorf("FAIL: moved directory listing = %v, err=%v", entries, err)
			}
		})
	}
}

func TestBackendDirectoryRenameRejectsSelfDescendant(t *testing.T) {
	b := memory.New()
	if err := b.MkdirAll("/src/child", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := b.Rename("/src", "/src/child/moved"); err == nil {
		t.Fatal("FAIL: directory move into its own descendant succeeded")
	}
	for _, p := range []string{"/src", "/src/child"} {
		if _, err := b.Stat(p); err != nil {
			t.Errorf("rejected rename changed %s: %v", p, err)
		}
	}
	if err := b.Rename("/src", "/src"); err != nil {
		t.Fatalf("same-path control: %v", err)
	}
}
