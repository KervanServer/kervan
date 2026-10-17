package main

import (
	"archive/zip"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kervanserver/kervan/internal/config"
	"github.com/kervanserver/kervan/internal/store"

	_ "modernc.org/sqlite"
)

func sqliteBackupConfig(t *testing.T) (cfgPath, dataDir string) {
	t.Helper()
	dataDir = filepath.Join(t.TempDir(), "data")
	cfgPath = writeTestConfig(t, func(cfg *config.Config) {
		cfg.Server.DataDir = dataDir
		cfg.Audit.Outputs = []config.AuditOutput{{Type: "file", Path: filepath.Join(dataDir, "audit.jsonl")}}
	})
	return cfgPath, dataDir
}

func createUser(t *testing.T, cfgPath, name string) {
	t.Helper()
	if err := runUserCreateCommand(io.Discard, []string{
		"--config", cfgPath, "--username", name, "--password", "StrongPass123!",
	}); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
}

func userExists(t *testing.T, cfgPath, name string) bool {
	t.Helper()
	ctx, err := openCLIContext(cfgPath)
	if err != nil {
		t.Fatalf("openCLIContext: %v", err)
	}
	defer ctx.close()
	u, err := ctx.repo.GetByUsername(name)
	if err != nil {
		t.Fatalf("get %s: %v", name, err)
	}
	return u != nil
}

func archiveNames(t *testing.T, path string) map[string]bool {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	out := map[string]bool{}
	for _, f := range r.File {
		out[f.Name] = true
	}
	return out
}

// Step 3: an SQLite installation is backed up as a snapshot of the live
// database, taken while another handle holds the store open, and restored
// over a damaged data directory.
func TestSQLiteBackupCreateRestoreRoundTrip(t *testing.T) {
	t.Setenv("KERVAN_STORE_BACKEND", "sqlite")
	cfgPath, dataDir := sqliteBackupConfig(t)
	createUser(t, cfgPath, "alice")

	// The "server" keeps the store open (WAL active) during the backup.
	live, err := store.OpenWithOptions(dataDir, store.Options{Backend: store.BackendSQLite})
	if err != nil {
		t.Fatal(err)
	}
	if err := live.Put("probe", "k", "v"); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "backup.zip")
	if err := runBackupCreateCommand(io.Discard, []string{"--config", cfgPath, "--output", archive}); err != nil {
		t.Fatalf("backup create: %v", err)
	}
	_ = live.Close()

	names := archiveNames(t, archive)
	if !names[backupStoreDBArchivePath] || names[backupStoreArchivePath] {
		t.Fatalf("SQLite backup entries = %v, want %s and no JSON store", names, backupStoreDBArchivePath)
	}
	if err := runBackupVerifyCommand(io.Discard, []string{"--input", archive}); err != nil {
		t.Fatalf("backup verify: %v", err)
	}

	// Changes after the backup are undone by the restore.
	createUser(t, cfgPath, "bob")
	if err := runBackupRestoreCommand(io.Discard, []string{"--config", cfgPath, "--input", archive}); err == nil {
		t.Fatal("restore over an existing database without --force was accepted")
	}
	if err := runBackupRestoreCommand(io.Discard, []string{"--config", cfgPath, "--input", archive, "--force"}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !userExists(t, cfgPath, "alice") || userExists(t, cfgPath, "bob") {
		t.Fatal("restored store does not match the backup")
	}
	matches, _ := filepath.Glob(filepath.Join(dataDir, ".kervan-restore-*"))
	if len(matches) != 0 {
		t.Fatalf("restore left temporary directories behind: %v", matches)
	}
}

// A backup taken while the installation still ran on the JSON store can be
// restored into an installation that has moved to SQLite.
func TestJSONBackupRestoresIntoSQLiteInstallation(t *testing.T) {
	cfgPath, dataDir := sqliteBackupConfig(t)
	t.Setenv("KERVAN_STORE_BACKEND", "json")
	createUser(t, cfgPath, "carol")
	archive := filepath.Join(t.TempDir(), "json-backup.zip")
	if err := runBackupCreateCommand(io.Discard, []string{"--config", cfgPath, "--output", archive}); err != nil {
		t.Fatalf("backup create (json): %v", err)
	}
	if !archiveNames(t, archive)[backupStoreArchivePath] {
		t.Fatal("JSON backup lacks the JSON store entry")
	}
	if err := os.RemoveAll(dataDir); err != nil {
		t.Fatal(err)
	}

	t.Setenv("KERVAN_STORE_BACKEND", "sqlite")
	if err := runBackupRestoreCommand(io.Discard, []string{"--config", cfgPath, "--input", archive}); err != nil {
		t.Fatalf("restore JSON archive into SQLite: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "kervan-store.db")); err != nil {
		t.Fatalf("no database after restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "kervan-store.json")); err == nil {
		t.Fatal("restore into an SQLite installation wrote kervan-store.json, which would be imported or ignored confusingly")
	}
	if !userExists(t, cfgPath, "carol") {
		t.Fatal("user from the JSON backup missing after restore into SQLite")
	}
}

// An SQLite archive is refused by a JSON installation (it cannot use it),
// and an archive holding both store entries is refused as ambiguous.
func TestBackupRestoreRefusesMismatchedStore(t *testing.T) {
	t.Setenv("KERVAN_STORE_BACKEND", "sqlite")
	cfgPath, dataDir := sqliteBackupConfig(t)
	createUser(t, cfgPath, "dave")
	archive := filepath.Join(t.TempDir(), "backup.zip")
	if err := runBackupCreateCommand(io.Discard, []string{"--config", cfgPath, "--output", archive}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dataDir); err != nil {
		t.Fatal(err)
	}

	t.Setenv("KERVAN_STORE_BACKEND", "json")
	err := runBackupRestoreCommand(io.Discard, []string{"--config", cfgPath, "--input", archive, "--force"})
	if err == nil || !strings.Contains(err.Error(), "SQLite store") {
		t.Fatalf("JSON installation accepted an SQLite archive: %v", err)
	}
	if entries, _ := os.ReadDir(dataDir); len(entries) != 0 {
		t.Fatalf("refused restore wrote into the data directory: %v", entries)
	}
}

// verify and restore reject an archived database that is not sound, even
// when its manifest checksum was rewritten to match.
func TestSQLiteBackupRejectsCorruptDatabase(t *testing.T) {
	t.Setenv("KERVAN_STORE_BACKEND", "sqlite")
	cfgPath, dataDir := sqliteBackupConfig(t)
	createUser(t, cfgPath, "erin")
	archive := filepath.Join(t.TempDir(), "backup.zip")
	if err := runBackupCreateCommand(io.Discard, []string{"--config", cfgPath, "--output", archive}); err != nil {
		t.Fatal(err)
	}
	// Strip the manifest so checksums cannot catch it, then replace the
	// database with garbage: only the SQLite check stands in the way.
	noManifest := filepath.Join(t.TempDir(), "no-manifest.zip")
	if err := removeBackupEntry(archive, noManifest, backupManifestPath); err != nil {
		t.Fatal(err)
	}
	corrupt := filepath.Join(t.TempDir(), "corrupt.zip")
	if err := rewriteBackupEntry(noManifest, corrupt, backupStoreDBArchivePath, []byte(strings.Repeat("x", 4096))); err != nil {
		t.Fatal(err)
	}
	if err := runBackupVerifyCommand(io.Discard, []string{"--input", corrupt}); err == nil {
		t.Fatal("verify accepted a corrupt database")
	}
	if err := os.RemoveAll(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := runBackupRestoreCommand(io.Discard, []string{"--config", cfgPath, "--input", corrupt, "--force"}); err == nil {
		t.Fatal("restore accepted a corrupt database")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "kervan-store.db")); err == nil {
		t.Fatal("refused restore left a database behind")
	}
}

// Restoring over a database whose -wal file still holds commits (a crash
// left it un-checkpointed) must not replay that journal onto the restored
// database: SQLite would bring back changes made after the backup.
func TestSQLiteRestoreDiscardsStaleJournal(t *testing.T) {
	t.Setenv("KERVAN_STORE_BACKEND", "sqlite")
	cfgPath, dataDir := sqliteBackupConfig(t)
	createUser(t, cfgPath, "frank")
	archive := filepath.Join(t.TempDir(), "backup.zip")
	if err := runBackupCreateCommand(io.Discard, []string{"--config", cfgPath, "--output", archive}); err != nil {
		t.Fatal(err)
	}

	// After the backup, a change is committed to the WAL and the process
	// "crashes" before a checkpoint: copy db+wal as they are on disk while
	// the store is still open, then put that state in place.
	live, err := store.OpenWithOptions(dataDir, store.Options{Backend: store.BackendSQLite})
	if err != nil {
		t.Fatal(err)
	}
	if err := live.Put("probe", "after-backup", "must not survive the restore"); err != nil {
		t.Fatal(err)
	}
	crashed := t.TempDir()
	for _, name := range []string{"kervan-store.db", "kervan-store.db-wal"} {
		raw, err := os.ReadFile(filepath.Join(dataDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(crashed, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_ = live.Close()
	for _, name := range []string{"kervan-store.db", "kervan-store.db-wal"} {
		raw, _ := os.ReadFile(filepath.Join(crashed, name))
		if err := os.WriteFile(filepath.Join(dataDir, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if info, err := os.Stat(filepath.Join(dataDir, "kervan-store.db-wal")); err != nil || info.Size() == 0 {
		t.Fatalf("test setup: expected a non-empty stale WAL, got %v %v", info, err)
	}

	if err := runBackupRestoreCommand(io.Discard, []string{"--config", cfgPath, "--input", archive, "--force"}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored, err := store.OpenWithOptions(dataDir, store.Options{Backend: store.BackendSQLite})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var v string
	if err := restored.Get("probe", "after-backup", &v); err == nil {
		t.Fatalf("a change made after the backup survived the restore (%q): the stale WAL was replayed", v)
	}
	if !userExists(t, cfgPath, "frank") {
		t.Fatal("restored store is missing the backed-up user")
	}
}

// sqliteFixtureDB builds a SQLite database file from DDL and returns its bytes.
func sqliteFixtureDB(t *testing.T, ddl ...string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range ddl {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			t.Fatalf("build fixture (%s): %v", stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// replaceArchiveEntryAndFixManifest swaps an archive entry AND rewrites the
// manifest so its recorded size and SHA-256 match the replacement. Without the
// re-signing the integrity check would reject the archive first, and the
// archive would prove nothing about the database check behind it.
func replaceArchiveEntryAndFixManifest(t *testing.T, srcPath, dstPath, entryPath string, content []byte) {
	t.Helper()
	reader, err := zip.OpenReader(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	var parsed backupManifest
	for _, f := range reader.File {
		if f.Name != backupManifestPath {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(io.LimitReader(rc, backupManifestMaxBytes))
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Fatal(err)
		}
	}
	sum := sha256.Sum256(content)
	found := false
	for i := range parsed.Files {
		if parsed.Files[i].ArchivePath == entryPath {
			parsed.Files[i].Size = int64(len(content))
			parsed.Files[i].SHA256 = hex.EncodeToString(sum[:])
			found = true
		}
	}
	if !found {
		t.Fatalf("manifest has no entry for %s", entryPath)
	}
	manifestRaw, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	out, err := os.Create(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	writer := zip.NewWriter(out)
	for _, file := range reader.File {
		header := file.FileHeader
		entry, err := writer.CreateHeader(&header)
		if err != nil {
			t.Fatal(err)
		}
		switch file.Name {
		case entryPath:
			if _, err := entry.Write(content); err != nil {
				t.Fatal(err)
			}
		case backupManifestPath:
			if _, err := entry.Write(manifestRaw); err != nil {
				t.Fatal(err)
			}
		default:
			rc, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(entry, rc); err != nil {
				t.Fatal(err)
			}
			_ = rc.Close()
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}

// A database is only a Kervan store if its kv table has the columns the store
// reads and writes. The table's NAME is not enough: a sound SQLite file with
// `kv(x TEXT)` passes an integrity check and a name lookup, so verify reported
// success and restore installed a store that failed on its first read and
// write ("no such column: v", "table kv has no column named coll").
func TestSQLiteBackupVerifyRejectsWrongKVSchema(t *testing.T) {
	t.Setenv("KERVAN_STORE_BACKEND", "sqlite")
	cfgPath, dataDir := sqliteBackupConfig(t)
	createUser(t, cfgPath, "hank")
	pristine := filepath.Join(t.TempDir(), "backup.zip")
	if err := runBackupCreateCommand(io.Discard, []string{"--config", cfgPath, "--output", pristine}); err != nil {
		t.Fatal(err)
	}

	// CONTROL: the untouched archive still verifies.
	if err := runBackupVerifyCommand(io.Discard, []string{"--input", pristine}); err != nil {
		t.Fatalf("pristine archive rejected: %v", err)
	}

	for _, tc := range []struct {
		name string
		ddl  []string
	}{
		{
			name: "kv table with the wrong columns",
			ddl: []string{
				`CREATE TABLE kv (x TEXT)`,
				`CREATE TABLE meta (k TEXT NOT NULL PRIMARY KEY, v TEXT NOT NULL) WITHOUT ROWID`,
			},
		},
		{
			name: "kv table missing only the v column",
			ddl:  []string{`CREATE TABLE kv (coll TEXT NOT NULL, k TEXT NOT NULL, PRIMARY KEY (coll, k))`},
		},
		{
			name: "no kv table at all",
			ddl:  []string{`CREATE TABLE unrelated (a TEXT)`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tampered := filepath.Join(t.TempDir(), "tampered.zip")
			replaceArchiveEntryAndFixManifest(t, pristine, tampered, backupStoreDBArchivePath, sqliteFixtureDB(t, tc.ddl...))

			verifyErr := runBackupVerifyCommand(io.Discard, []string{"--input", tampered})
			if verifyErr == nil {
				t.Fatalf("verify accepted a database that is not a usable store (%s)", tc.name)
			}
			if !strings.Contains(verifyErr.Error(), "not a Kervan store database") {
				t.Fatalf("verify failed for the wrong reason: %v", verifyErr)
			}

			restoreErr := runBackupRestoreCommand(io.Discard, []string{"--config", cfgPath, "--input", tampered, "--force"})
			if restoreErr == nil {
				t.Fatalf("restore accepted a database that is not a usable store (%s)", tc.name)
			}
			st, err := store.OpenWithOptions(dataDir, store.Options{Backend: store.BackendSQLite})
			if err != nil {
				t.Fatalf("open restored store: %v", err)
			}
			defer st.Close()
			if err := st.Put("probe", "k", "v"); err != nil {
				t.Fatalf("restored store is unusable, but the tampered archive was already refused: %v", err)
			}
		})
	}
}
