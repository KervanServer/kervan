package vfs_test

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/vfs"
)

func TestMountTableLookupAndListMountPoints(t *testing.T) {
	mt := vfs.NewMountTable()
	root := memory.New()
	archive := memory.New()
	mt.Mount("/", root, false)
	mt.Mount("/archive", archive, true)
	mt.Mount("/archive/deeper", root, false)

	backend, rel, ro, err := mt.Lookup("/archive/deeper/file.txt")
	if err != nil {
		t.Fatalf("lookup deeper mount: %v", err)
	}
	if backend != root {
		t.Fatal("expected longest-prefix mount to win")
	}
	if rel != "/file.txt" {
		t.Fatalf("unexpected relative path: %q", rel)
	}
	if ro {
		t.Fatal("expected deeper mount to be writable")
	}

	points := mt.ListMountPoints("/archive")
	if len(points) != 1 || points[0] != "deeper" {
		t.Fatalf("unexpected mount points: %v", points)
	}

	if _, _, _, err := vfs.NewMountTable().Lookup("/missing"); !errors.Is(err, vfs.ErrNoMount) {
		t.Fatalf("expected ErrNoMount, got %v", err)
	}
}

func TestResolverResolvePairAndDepth(t *testing.T) {
	r := vfs.NewResolver()

	from, to, err := r.ResolvePair("/docs/a.txt", "/docs/b.txt")
	if err != nil {
		t.Fatalf("resolve pair: %v", err)
	}
	if from != "/docs/a.txt" || to != "/docs/b.txt" {
		t.Fatalf("unexpected resolved pair: from=%q to=%q", from, to)
	}

	tooDeep := "/" + strings.TrimSuffix(strings.Repeat("a/", 257), "/")
	if _, err := r.Resolve(tooDeep); !errors.Is(err, vfs.ErrPathTooDeep) {
		t.Fatalf("expected ErrPathTooDeep, got %v", err)
	}
}

func TestUserVFSMountPermissionsAndOperations(t *testing.T) {
	root := memory.New()
	readOnly := memory.New()

	rootFile, err := root.Open("/existing.txt", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("seed root file: %v", err)
	}
	if _, err := rootFile.Write([]byte("hello")); err != nil {
		t.Fatalf("write root file: %v", err)
	}
	_ = rootFile.Close()

	roFile, err := readOnly.Open("/frozen.txt", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("seed readonly file: %v", err)
	}
	if _, err := roFile.Write([]byte("archive")); err != nil {
		t.Fatalf("write readonly file: %v", err)
	}
	_ = roFile.Close()

	mounts := vfs.NewMountTable()
	mounts.Mount("/", root, false)
	mounts.Mount("/archive", readOnly, true)

	fsys := vfs.NewUserVFS(mounts, &vfs.UserPermissions{
		Upload:      true,
		Download:    true,
		Delete:      true,
		Rename:      true,
		CreateDir:   true,
		ListDir:     true,
		Chmod:       true,
		AllowedExts: []string{".txt"},
		DeniedExts:  []string{".exe"},
	}, nil)

	entries, err := fsys.ReadDir("/")
	if err != nil {
		t.Fatalf("readdir root: %v", err)
	}
	foundArchive := false
	for _, entry := range entries {
		if entry.Name() == "archive" {
			foundArchive = true
			break
		}
	}
	if !foundArchive {
		t.Fatalf("expected synthetic archive mount point in root listing: %v", entries)
	}

	file, err := fsys.Open("/good.txt", os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open good.txt: %v", err)
	}
	if _, err := file.Write([]byte("payload")); err != nil {
		t.Fatalf("write good.txt: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close good.txt: %v", err)
	}

	if _, err := fsys.Open("/bad.exe", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644); !errors.Is(err, vfs.ErrForbiddenExtension) {
		t.Fatalf("expected forbidden extension error, got %v", err)
	}
	if _, err := fsys.Open("/archive/new.txt", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected readonly mount write denial, got %v", err)
	}
	if err := fsys.Rename("/good.txt", "/archive/good.txt"); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected cross-mount rename denial, got %v", err)
	}
	if err := fsys.RemoveAll("/archive"); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected readonly removeall denial, got %v", err)
	}

	now := time.Now().Add(-time.Minute).UTC()
	if err := fsys.Chmod("/good.txt", 0o600); err != nil {
		t.Fatalf("chmod good.txt: %v", err)
	}
	if err := fsys.Chtimes("/good.txt", now, now); err != nil {
		t.Fatalf("chtimes good.txt: %v", err)
	}
	if err := fsys.Chown("/good.txt", 1, 1); err != nil {
		t.Fatalf("chown good.txt: %v", err)
	}
	if _, err := fsys.Statvfs("/good.txt"); err != nil {
		t.Fatalf("statvfs good.txt: %v", err)
	}
	if _, err := fsys.Stat("/good.txt"); err != nil {
		t.Fatalf("stat good.txt: %v", err)
	}
	if _, err := fsys.Lstat("/good.txt"); err != nil {
		t.Fatalf("lstat good.txt: %v", err)
	}
	if err := fsys.Remove("/good.txt"); err != nil {
		t.Fatalf("remove good.txt: %v", err)
	}
}

func TestUserVFSPermissionDenials(t *testing.T) {
	root := memory.New()
	file, err := root.Open("/readme.txt", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := file.Write([]byte("hello")); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	_ = file.Close()

	mounts := vfs.NewMountTable()
	mounts.Mount("/", root, false)

	noRead := vfs.NewUserVFS(mounts, &vfs.UserPermissions{Download: false}, nil)
	if _, err := noRead.Open("/readme.txt", os.O_RDONLY, 0); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected download denial, got %v", err)
	}

	noWrite := vfs.NewUserVFS(mounts, &vfs.UserPermissions{Upload: false, Download: true}, nil)
	if _, err := noWrite.Open("/new.txt", os.O_CREATE|os.O_WRONLY, 0o644); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected upload denial, got %v", err)
	}

	noList := vfs.NewUserVFS(mounts, &vfs.UserPermissions{ListDir: false}, nil)
	if _, err := noList.ReadDir("/"); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected listdir denial, got %v", err)
	}

	noMkdir := vfs.NewUserVFS(mounts, &vfs.UserPermissions{CreateDir: false}, nil)
	if err := noMkdir.Mkdir("/blocked", 0o755); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected mkdir denial, got %v", err)
	}

	noDelete := vfs.NewUserVFS(mounts, &vfs.UserPermissions{Delete: false}, nil)
	if err := noDelete.Remove("/readme.txt"); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected delete denial, got %v", err)
	}

	noRename := vfs.NewUserVFS(mounts, &vfs.UserPermissions{Rename: false}, nil)
	if err := noRename.Rename("/readme.txt", "/other.txt"); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected rename denial, got %v", err)
	}

	noChmod := vfs.NewUserVFS(mounts, &vfs.UserPermissions{Chmod: false}, nil)
	if err := noChmod.Chmod("/readme.txt", 0o600); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected chmod denial, got %v", err)
	}
}
