package memory_test

import (
	"io"
	"os"
	"testing"

	"github.com/kervanserver/kervan/internal/storage/local"
	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/vfs"
)

// Renaming onto an existing destination follows the replace contract of
// UserVFS.Rename and rename(2): a file may replace an existing file, and a
// directory may replace an empty directory. Every other existing-target
// combination must be rejected; before that guard the memory backend silently
// overwrote the destination's map entry while the old destination subtree
// stayed keyed under the destination path — orphaned but still directly
// reachable, invisible to listings, and still counted by quotas. The local
// backend is the oracle for the rejected combinations (universal POSIX
// errors), so those legs run on both backends; the dir-over-empty-dir
// replacement is asserted on memory only because overlayfs-backed tempdirs
// return EEXIST where rename(2) would succeed.

func newDestinationRenameFS(t *testing.T, kind string) vfs.FileSystem {
	t.Helper()
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
	return vfs.NewUserVFS(mounts, &vfs.UserPermissions{
		Upload: true, Download: true, Delete: true, Rename: true,
		CreateDir: true, ListDir: true,
	}, nil)
}

func writeDestinationTestFile(t *testing.T, fsys vfs.FileSystem, p, content string) {
	t.Helper()
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

func readDestinationTestFile(t *testing.T, fsys vfs.FileSystem, p string) string {
	t.Helper()
	f, err := fsys.Open(p, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(data)
}

func TestBackendRenameOntoExistingDirectoryIsRejected(t *testing.T) {
	for _, kind := range []string{"memory", "local_control"} {
		t.Run(kind, func(t *testing.T) {

			t.Run("file onto existing directory", func(t *testing.T) {
				fsys := newDestinationRenameFS(t, kind)
				if err := fsys.MkdirAll("/victim", 0o755); err != nil {
					t.Fatal(err)
				}
				writeDestinationTestFile(t, fsys, "/victim/child.txt", "child-content")
				writeDestinationTestFile(t, fsys, "/file.txt", "file-content")
				if err := fsys.Rename("/file.txt", "/victim"); err == nil {
					t.Fatalf("FAIL: renaming a file onto an existing directory succeeded; want rejection like the local/POSIX backend")
				}
				if info, statErr := fsys.Stat("/victim"); statErr != nil || !info.IsDir() {
					t.Errorf("FAIL: refused rename left /victim as a non-directory: info=%v err=%v", info, statErr)
				}
				if got := readDestinationTestFile(t, fsys, "/victim/child.txt"); got != "child-content" {
					t.Errorf("FAIL: /victim/child.txt = %q, want %q", got, "child-content")
				}
				if got := readDestinationTestFile(t, fsys, "/file.txt"); got != "file-content" {
					t.Errorf("FAIL: source /file.txt lost after refused rename: %q", got)
				}
			})

			t.Run("directory onto existing file", func(t *testing.T) {
				fsys := newDestinationRenameFS(t, kind)
				if err := fsys.MkdirAll("/srcd", 0o755); err != nil {
					t.Fatal(err)
				}
				writeDestinationTestFile(t, fsys, "/srcd/inner.txt", "inner")
				writeDestinationTestFile(t, fsys, "/target.txt", "target")
				if err := fsys.Rename("/srcd", "/target.txt"); err == nil {
					t.Fatalf("FAIL: renaming a directory onto an existing file succeeded; want rejection like the local/POSIX backend")
				}
				if got := readDestinationTestFile(t, fsys, "/target.txt"); got != "target" {
					t.Errorf("FAIL: /target.txt = %q, want %q", got, "target")
				}
				if got := readDestinationTestFile(t, fsys, "/srcd/inner.txt"); got != "inner" {
					t.Errorf("FAIL: /srcd/inner.txt = %q, want %q", got, "inner")
				}
			})

			t.Run("directory onto existing non-empty directory", func(t *testing.T) {
				fsys := newDestinationRenameFS(t, kind)
				for _, dir := range []string{"/a", "/b"} {
					if err := fsys.MkdirAll(dir, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				writeDestinationTestFile(t, fsys, "/a/keep.txt", "keep")
				writeDestinationTestFile(t, fsys, "/b/other.txt", "other")
				if err := fsys.Rename("/a", "/b"); err == nil {
					t.Fatalf("FAIL: renaming a directory onto a non-empty directory succeeded; want rejection like the local/POSIX backend")
				}
				if got := readDestinationTestFile(t, fsys, "/b/other.txt"); got != "other" {
					t.Errorf("FAIL: /b/other.txt = %q, want %q", got, "other")
				}
				if got := readDestinationTestFile(t, fsys, "/a/keep.txt"); got != "keep" {
					t.Errorf("FAIL: /a/keep.txt = %q, want %q", got, "keep")
				}
			})

			t.Run("control file onto existing file replaces", func(t *testing.T) {
				fsys := newDestinationRenameFS(t, kind)
				writeDestinationTestFile(t, fsys, "/one.txt", "one")
				writeDestinationTestFile(t, fsys, "/two.txt", "two")
				if err := fsys.Rename("/one.txt", "/two.txt"); err != nil {
					t.Fatalf("control: file-over-file rename failed: %v", err)
				}
				if got := readDestinationTestFile(t, fsys, "/two.txt"); got != "one" {
					t.Errorf("control: /two.txt = %q, want %q", got, "one")
				}
				if _, err := fsys.Stat("/one.txt"); !os.IsNotExist(err) {
					t.Errorf("control: /one.txt still exists after rename: %v", err)
				}
			})

			t.Run("control directory rename to a fresh path succeeds", func(t *testing.T) {
				fsys := newDestinationRenameFS(t, kind)
				if err := fsys.MkdirAll("/fresh-src/inner", 0o755); err != nil {
					t.Fatal(err)
				}
				writeDestinationTestFile(t, fsys, "/fresh-src/inner/data.txt", "data")
				if err := fsys.Rename("/fresh-src", "/fresh-dst"); err != nil {
					t.Fatalf("control: directory rename to a fresh path failed: %v", err)
				}
				if got := readDestinationTestFile(t, fsys, "/fresh-dst/inner/data.txt"); got != "data" {
					t.Errorf("control: /fresh-dst/inner/data.txt = %q, want %q", got, "data")
				}
			})

		})
	}
}

func TestBackendRenameDirectoryOntoEmptyDirectoryReplaces(t *testing.T) {
	fsys := newDestinationRenameFS(t, "memory")
	for _, dir := range []string{"/empty-src", "/empty-dst"} {
		if err := fsys.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeDestinationTestFile(t, fsys, "/empty-src/file.txt", "moved")
	if err := fsys.Rename("/empty-src", "/empty-dst"); err != nil {
		t.Fatalf("renaming a directory onto an empty directory failed: %v", err)
	}
	if got := readDestinationTestFile(t, fsys, "/empty-dst/file.txt"); got != "moved" {
		t.Errorf("/empty-dst/file.txt = %q, want %q", got, "moved")
	}
	if _, err := fsys.Stat("/empty-src"); !os.IsNotExist(err) {
		t.Errorf("/empty-src still exists after replacement: %v", err)
	}
}

func TestBackendRenameOntoRootIsRejected(t *testing.T) {
	fsys := newDestinationRenameFS(t, "memory")
	if err := fsys.MkdirAll("/tree/inner", 0o755); err != nil {
		t.Fatal(err)
	}
	writeDestinationTestFile(t, fsys, "/tree/inner/data.txt", "data")
	if err := fsys.Rename("/tree", "/"); err == nil {
		t.Fatal("FAIL: renaming a directory onto the root succeeded; want rejection")
	}
	if _, err := fsys.Stat("/tree/inner/data.txt"); err != nil {
		t.Errorf("refused rename lost /tree/inner/data.txt: %v", err)
	}
	writeDestinationTestFile(t, fsys, "/plain.txt", "plain")
	if err := fsys.Rename("/plain.txt", "/"); err == nil {
		t.Fatal("FAIL: renaming a file onto the root succeeded; want rejection")
	}
	if got := readDestinationTestFile(t, fsys, "/plain.txt"); got != "plain" {
		t.Errorf("/plain.txt = %q after refused rename, want %q", got, "plain")
	}
}
