package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kervanserver/kervan/internal/audit"
	"github.com/kervanserver/kervan/internal/config"
	"github.com/kervanserver/kervan/internal/store"
)

const (
	backupStoreArchivePath    = "store/kervan-store.json"
	backupStoreBakArchivePath = "store/kervan-store.json.bak"
	// backupStoreDBArchivePath holds a consistent snapshot of the SQLite
	// store (kervan-store.db). An archive carries either it or the JSON
	// store entry, depending on the backend the installation runs.
	backupStoreDBArchivePath  = "store/kervan-store.db"
	backupAuditArchivePath    = "audit/audit.jsonl"
	backupAuditKeyArchivePath = "audit/audit.key"
	backupAuditRotatedPrefix  = "audit/rotated/"
	backupConfigArchivePath   = "config/kervan.yaml"
	backupManifestPath        = "manifest.json"
	backupManifestMaxBytes    = 2 << 20
	backupEntryMaxBytes       = 1 << 30
)

type backupManifest struct {
	Version     int                   `json:"version"`
	GeneratedAt string                `json:"generated_at"`
	ConfigPath  string                `json:"config_path"`
	DataDir     string                `json:"data_dir"`
	Files       []backupManifestEntry `json:"files"`
}

type backupManifestEntry struct {
	ArchivePath string `json:"archive_path"`
	SourcePath  string `json:"source_path"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
}

type backupArchiveFile struct {
	ArchivePath string
	SourcePath  string
	Required    bool
}

type backupRestoreEntry struct {
	ArchivePath string
	TargetPath  string
	Optional    bool
}

func cmdBackup(args []string) {
	if err := runBackupCommand(os.Stdout, args); err != nil {
		exitf("backup: %v", err)
	}
}

func runBackupCommand(stdout io.Writer, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: kervan backup <create|restore|verify> [flags]")
	}
	switch args[0] {
	case "create":
		return runBackupCreateCommand(stdout, args[1:])
	case "restore":
		return runBackupRestoreCommand(stdout, args[1:])
	case "verify":
		return runBackupVerifyCommand(stdout, args[1:])
	default:
		return fmt.Errorf("unknown backup command: %s", args[0])
	}
}

func runBackupCreateCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("backup create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	outputPath := fs.String("output", defaultBackupArchiveName(), "Output ZIP archive")
	includeAudit := fs.Bool("include-audit", true, "Include audit log if present")
	includeConfig := fs.Bool("include-config", true, "Include config file")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse backup create flags: %w", err)
	}
	if strings.TrimSpace(*outputPath) == "" {
		return errors.New("--output is required")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config %s: %w", *configPath, err)
	}

	var files []backupArchiveFile
	if store.ResolveBackend("") == store.BackendSQLite {
		// The database is live (WAL, possibly other processes writing), so
		// it is never copied as a file: a VACUUM INTO snapshot is taken
		// instead and archived.
		snapshot, cleanup, err := snapshotSQLiteStore(cfg.Server.DataDir)
		if err != nil {
			return err
		}
		defer cleanup()
		files = append(files, backupArchiveFile{
			ArchivePath: backupStoreDBArchivePath,
			SourcePath:  snapshot,
			Required:    true,
		})
	} else {
		files = append(files, backupArchiveFile{
			ArchivePath: backupStoreArchivePath,
			SourcePath:  filepath.Join(cfg.Server.DataDir, "kervan-store.json"),
			Required:    true,
		}, backupArchiveFile{
			ArchivePath: backupStoreBakArchivePath,
			SourcePath:  filepath.Join(cfg.Server.DataDir, "kervan-store.json.bak"),
		})
	}
	if *includeAudit {
		files = append(files, backupArchiveFile{
			ArchivePath: backupAuditArchivePath,
			SourcePath:  backupAuditPath(cfg),
		}, backupArchiveFile{
			// Without the integrity key a restored log cannot be verified.
			ArchivePath: backupAuditKeyArchivePath,
			SourcePath:  cfg.AuditKeyPath(),
		})
		rotated, err := audit.RotatedFiles(backupAuditPath(cfg))
		if err != nil {
			return fmt.Errorf("list rotated audit files: %w", err)
		}
		for _, file := range rotated {
			files = append(files, backupArchiveFile{
				ArchivePath: backupAuditRotatedPrefix + filepath.Base(file),
				SourcePath:  file,
			})
		}
	}
	if *includeConfig {
		files = append(files, backupArchiveFile{
			ArchivePath: backupConfigArchivePath,
			SourcePath:  *configPath,
		})
	}

	if err := os.MkdirAll(filepath.Dir(*outputPath), 0o750); err != nil {
		return fmt.Errorf("create output directory for %s: %w", *outputPath, err)
	}

	out, err := os.CreateTemp(filepath.Dir(*outputPath), filepath.Base(*outputPath)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary backup archive: %w", err)
	}
	tmpPath := out.Name()
	defer func() {
		_ = out.Close()
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()

	zipWriter := zip.NewWriter(out)
	manifest := backupManifest{
		Version:     2,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		ConfigPath:  *configPath,
		DataDir:     cfg.Server.DataDir,
	}
	included := make([]string, 0, len(files))

	for _, file := range files {
		// #nosec G304 -- backup source paths are derived from local config/data directories.
		source, err := os.Open(file.SourcePath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && !file.Required {
				continue
			}
			return fmt.Errorf("read %s: %w", file.SourcePath, err)
		}
		entry, err := zipWriter.Create(file.ArchivePath)
		if err != nil {
			_ = source.Close()
			return fmt.Errorf("create archive entry %s: %w", file.ArchivePath, err)
		}
		digest := sha256.New()
		size, err := io.Copy(io.MultiWriter(entry, digest), source)
		_ = source.Close()
		if err != nil {
			return fmt.Errorf("write archive entry %s: %w", file.ArchivePath, err)
		}
		included = append(included, file.ArchivePath)
		sourcePath := file.SourcePath
		if file.ArchivePath == backupStoreDBArchivePath {
			sourcePath = filepath.Join(cfg.Server.DataDir, store.FileName(store.BackendSQLite))
		}
		manifest.Files = append(manifest.Files, backupManifestEntry{
			ArchivePath: file.ArchivePath,
			SourcePath:  sourcePath,
			Size:        size,
			SHA256:      hex.EncodeToString(digest.Sum(nil)),
		})
	}

	manifestRaw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal backup manifest: %w", err)
	}
	manifestEntry, err := zipWriter.Create(backupManifestPath)
	if err != nil {
		return fmt.Errorf("create archive entry %s: %w", backupManifestPath, err)
	}
	if _, err := manifestEntry.Write(manifestRaw); err != nil {
		return fmt.Errorf("write archive entry %s: %w", backupManifestPath, err)
	}
	if err := zipWriter.Close(); err != nil {
		return fmt.Errorf("finalize backup archive: %w", err)
	}
	if err := out.Chmod(0o600); err != nil {
		return fmt.Errorf("set backup archive permissions: %w", err)
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("sync temporary backup archive: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close temporary backup archive: %w", err)
	}
	if err := store.ReplaceTempFileAtomically(tmpPath, *outputPath); err != nil {
		return fmt.Errorf("move backup archive into place: %w", err)
	}
	tmpPath = ""

	payload := map[string]any{
		"created":        true,
		"output":         *outputPath,
		"included_files": included,
	}
	if *jsonOut {
		if err := json.NewEncoder(stdout).Encode(payload); err != nil {
			return fmt.Errorf("encode backup create output: %w", err)
		}
		return nil
	}

	_, _ = fmt.Fprintf(stdout, "Backup created: %s\n", *outputPath)
	_, _ = fmt.Fprintf(stdout, "Included files: %d\n", len(included))
	return nil
}

func runBackupRestoreCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("backup restore", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	inputPath := fs.String("input", "", "Backup ZIP archive")
	force := fs.Bool("force", false, "Overwrite existing files")
	restoreConfig := fs.Bool("restore-config", false, "Restore config/kervan.yaml into --config path")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse backup restore flags: %w", err)
	}
	if strings.TrimSpace(*inputPath) == "" {
		return errors.New("--input is required")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config %s: %w", *configPath, err)
	}

	reader, err := zip.OpenReader(*inputPath)
	if err != nil {
		return fmt.Errorf("open backup archive %s: %w", *inputPath, err)
	}
	defer reader.Close()

	archiveEntries, manifest, err := loadBackupArchive(reader.File)
	if err != nil {
		return fmt.Errorf("load backup archive %s: %w", *inputPath, err)
	}

	// The store entry decides how the store is restored. The other targets
	// (audit log, key, rotated files, config) are plain files either way.
	sqliteDeployment := store.ResolveBackend("") == store.BackendSQLite
	_, hasJSONStore := archiveEntries[backupStoreArchivePath]
	_, hasDBStore := archiveEntries[backupStoreDBArchivePath]
	targets := map[string]backupRestoreEntry{
		backupAuditArchivePath: {
			ArchivePath: backupAuditArchivePath,
			TargetPath:  backupAuditPath(cfg),
			Optional:    true,
		},
		backupAuditKeyArchivePath: {
			ArchivePath: backupAuditKeyArchivePath,
			TargetPath:  cfg.AuditKeyPath(),
			Optional:    true,
		},
	}
	if *restoreConfig {
		targets[backupConfigArchivePath] = backupRestoreEntry{
			ArchivePath: backupConfigArchivePath,
			TargetPath:  *configPath,
			Optional:    true,
		}
	}

	// Rotated audit files return next to the live log; names are checked
	// so an archive cannot write elsewhere.
	auditPath := backupAuditPath(cfg)
	for archivePath := range archiveEntries {
		base, ok := strings.CutPrefix(archivePath, backupAuditRotatedPrefix)
		if !ok || !audit.IsRotatedFileOf(auditPath, base) {
			continue
		}
		targets[archivePath] = backupRestoreEntry{
			ArchivePath: archivePath,
			TargetPath:  filepath.Join(filepath.Dir(auditPath), base),
			Optional:    true,
		}
	}

	// Check the store entry BEFORE writing anything. A restore that is going
	// to be rejected must not leave recovered files behind, and the optional
	// targets below (audit log, audit key, store .bak) are written during the
	// same loop.
	switch {
	case hasJSONStore && hasDBStore:
		return fmt.Errorf("backup archive holds both %s and %s; refusing an ambiguous store", backupStoreArchivePath, backupStoreDBArchivePath)
	case !hasJSONStore && !hasDBStore:
		return fmt.Errorf("backup archive missing required entry %s (or %s)", backupStoreArchivePath, backupStoreDBArchivePath)
	case hasDBStore && !sqliteDeployment:
		return fmt.Errorf("backup archive holds an SQLite store (%s), but this installation uses the JSON store; set KERVAN_STORE_BACKEND=sqlite to restore it", backupStoreDBArchivePath)
	}

	// An SQLite store is restored as a whole database file. It is prepared
	// and verified next to its target before anything is written: from the
	// archived database, or by importing an archived JSON store (a backup
	// taken before the installation moved to SQLite).
	var preparedDB string
	if sqliteDeployment {
		dbTarget := filepath.Join(cfg.Server.DataDir, store.FileName(store.BackendSQLite))
		if !*force {
			if _, err := os.Stat(dbTarget); err == nil {
				return fmt.Errorf("refusing to overwrite existing file %s without --force", dbTarget)
			}
		}
		prepared, cleanup, err := prepareSQLiteRestore(archiveEntries, cfg.Server.DataDir, hasDBStore)
		if err != nil {
			return err
		}
		defer cleanup()
		preparedDB = prepared
	} else {
		// Verify the store files can be opened BEFORE anything is written,
		// as the SQLite branch does. Restoring an unreadable store succeeds
		// and leaves an installation that cannot start.
		if err := verifyArchivedJSONStore(archiveEntries); err != nil {
			return err
		}
		targets[backupStoreArchivePath] = backupRestoreEntry{
			ArchivePath: backupStoreArchivePath,
			TargetPath:  filepath.Join(cfg.Server.DataDir, "kervan-store.json"),
		}
		targets[backupStoreBakArchivePath] = backupRestoreEntry{
			ArchivePath: backupStoreBakArchivePath,
			TargetPath:  filepath.Join(cfg.Server.DataDir, "kervan-store.json.bak"),
			Optional:    true,
		}
	}

	restored := make([]string, 0, len(targets)+1)
	if preparedDB != "" {
		dbTarget := filepath.Join(cfg.Server.DataDir, store.FileName(store.BackendSQLite))
		if err := store.ReplaceSQLiteFile(preparedDB, dbTarget); err != nil {
			return fmt.Errorf("restore store to %s: %w", dbTarget, err)
		}
		if hasDBStore {
			restored = append(restored, backupStoreDBArchivePath)
		} else {
			restored = append(restored, backupStoreArchivePath)
		}
	}
	for archivePath, target := range targets {
		file, ok := archiveEntries[archivePath]
		if !ok {
			continue
		}
		if err := restoreBackupFile(file, target.TargetPath, *force); err != nil {
			return fmt.Errorf("restore %s to %s: %w", archivePath, target.TargetPath, err)
		}
		restored = append(restored, archivePath)
	}

	payload := map[string]any{
		"restored":       true,
		"input":          *inputPath,
		"restored_files": restored,
		"verified":       manifest != nil && manifest.Version >= 2,
	}
	if *jsonOut {
		if err := json.NewEncoder(stdout).Encode(payload); err != nil {
			return fmt.Errorf("encode backup restore output: %w", err)
		}
		return nil
	}

	_, _ = fmt.Fprintf(stdout, "Backup restored: %s\n", *inputPath)
	_, _ = fmt.Fprintf(stdout, "Restored files: %d\n", len(restored))
	if manifest != nil && manifest.Version >= 2 {
		_, _ = fmt.Fprintln(stdout, "Integrity check: verified")
	}
	return nil
}

func runBackupVerifyCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("backup verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	inputPath := fs.String("input", "", "Backup ZIP archive")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse backup verify flags: %w", err)
	}
	if strings.TrimSpace(*inputPath) == "" {
		return errors.New("--input is required")
	}

	reader, err := zip.OpenReader(*inputPath)
	if err != nil {
		return fmt.Errorf("open backup archive %s: %w", *inputPath, err)
	}
	defer reader.Close()

	archiveEntries, manifest, err := loadBackupArchive(reader.File)
	if err != nil {
		return fmt.Errorf("load backup archive %s: %w", *inputPath, err)
	}
	_, hasJSONStore := archiveEntries[backupStoreArchivePath]
	dbEntry, hasDBStore := archiveEntries[backupStoreDBArchivePath]
	switch {
	case hasJSONStore && hasDBStore:
		return fmt.Errorf("backup archive holds both %s and %s", backupStoreArchivePath, backupStoreDBArchivePath)
	case !hasJSONStore && !hasDBStore:
		return fmt.Errorf("backup archive missing required entry %s (or %s)", backupStoreArchivePath, backupStoreDBArchivePath)
	case hasDBStore:
		// Checksums say the bytes are the ones archived; this says they
		// are a sound database.
		if err := verifyArchivedSQLiteStore(dbEntry); err != nil {
			return err
		}
	case hasJSONStore:
		// The same contract on the JSON side: the manifest proves the bytes
		// are the ones archived, this says a store can be opened from them.
		if err := verifyArchivedJSONStore(archiveEntries); err != nil {
			return err
		}
	}

	entryCount := len(archiveEntries)
	verified := manifest != nil && manifest.Version >= 2
	payload := map[string]any{
		"verified":         verified,
		"input":            *inputPath,
		"entry_count":      entryCount,
		"manifest_present": manifest != nil,
	}
	if manifest != nil {
		payload["manifest_version"] = manifest.Version
		payload["generated_at"] = manifest.GeneratedAt
		payload["verified_files"] = len(manifest.Files)
	} else {
		payload["verified_files"] = 0
	}
	if *jsonOut {
		if err := json.NewEncoder(stdout).Encode(payload); err != nil {
			return fmt.Errorf("encode backup verify output: %w", err)
		}
		return nil
	}

	if verified {
		_, _ = fmt.Fprintf(stdout, "Backup verified: %s\n", *inputPath)
	} else {
		_, _ = fmt.Fprintf(stdout, "Backup inspected: %s\n", *inputPath)
	}
	_, _ = fmt.Fprintf(stdout, "Archive entries: %d\n", entryCount)
	if manifest != nil {
		_, _ = fmt.Fprintf(stdout, "Integrity manifest: version %d (%d files)\n", manifest.Version, len(manifest.Files))
	} else {
		_, _ = fmt.Fprintln(stdout, "Integrity manifest: not present")
	}
	return nil
}

func loadBackupArchive(files []*zip.File) (map[string]*zip.File, *backupManifest, error) {
	entries := make(map[string]*zip.File, len(files))
	var manifest *backupManifest

	for _, file := range files {
		entries[file.Name] = file
		if file.Name != backupManifestPath {
			continue
		}
		raw, err := readZipFile(file, backupManifestMaxBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", backupManifestPath, err)
		}
		var parsed backupManifest
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, nil, fmt.Errorf("decode %s: %w", backupManifestPath, err)
		}
		manifest = &parsed
	}

	if manifest != nil {
		if err := verifyBackupManifest(entries, manifest); err != nil {
			return nil, nil, fmt.Errorf("verify backup manifest: %w", err)
		}
	}

	return entries, manifest, nil
}

func restoreBackupFile(file *zip.File, targetPath string, force bool) error {
	if !force {
		if _, err := os.Stat(targetPath); err == nil {
			return fmt.Errorf("refusing to overwrite existing file %s without --force", targetPath)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat restore target %s: %w", targetPath, err)
		}
	}

	rc, err := file.Open()
	if err != nil {
		return fmt.Errorf("open archive entry %s: %w", file.Name, err)
	}
	defer rc.Close()
	if file.UncompressedSize64 > uint64(backupEntryMaxBytes) {
		return fmt.Errorf("archive entry %s exceeds %d bytes", file.Name, backupEntryMaxBytes)
	}
	reader := io.Reader(rc)
	reader = io.LimitReader(reader, backupEntryMaxBytes+1)

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o750); err != nil {
		return fmt.Errorf("create restore directory for %s: %w", targetPath, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(targetPath), filepath.Base(targetPath)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary restore file for %s: %w", targetPath, err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()
	written, err := io.Copy(tmp, reader)
	if err != nil {
		return fmt.Errorf("write restored data for %s: %w", targetPath, err)
	}
	if written > backupEntryMaxBytes {
		return fmt.Errorf("archive entry %s exceeds %d bytes", file.Name, backupEntryMaxBytes)
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("set temporary restore file permissions for %s: %w", targetPath, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temporary restore file for %s: %w", targetPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary restore file for %s: %w", targetPath, err)
	}
	if err := store.ReplaceTempFileAtomically(tmpPath, targetPath); err != nil {
		return fmt.Errorf("replace restore target %s: %w", targetPath, err)
	}
	tmpPath = ""
	return nil
}

func verifyBackupManifest(entries map[string]*zip.File, manifest *backupManifest) error {
	if manifest == nil || manifest.Version < 2 {
		return nil
	}
	covered := make(map[string]struct{}, len(manifest.Files))
	for _, item := range manifest.Files {
		if strings.TrimSpace(item.ArchivePath) == "" {
			return errors.New("backup manifest contains empty archive_path")
		}
		entry, ok := entries[item.ArchivePath]
		if !ok {
			return fmt.Errorf("backup manifest references missing archive entry %s", item.ArchivePath)
		}
		size, checksum, err := zipFileChecksum(entry)
		if err != nil {
			return fmt.Errorf("read %s: %w", item.ArchivePath, err)
		}
		if item.Size != size {
			return fmt.Errorf("backup integrity check failed for %s: size mismatch", item.ArchivePath)
		}
		if want := normalizeChecksum(item.SHA256); want != "" {
			if checksum != want {
				return fmt.Errorf("backup integrity check failed for %s: checksum mismatch", item.ArchivePath)
			}
		}
		covered[item.ArchivePath] = struct{}{}
	}
	// The manifest is the integrity statement for the whole archive, so the
	// coverage has to hold both ways. An entry the manifest does not list is
	// checksum-verified by nothing, yet restore still writes it to disk and
	// reports "verified" — anyone able to append to an archive could plant
	// arbitrary files in server.data_dir that way.
	for name := range entries {
		if name == backupManifestPath {
			continue
		}
		if _, ok := covered[name]; !ok {
			return fmt.Errorf("backup archive contains entry not covered by the integrity manifest: %s", name)
		}
	}
	return nil
}

func zipFileChecksum(file *zip.File) (int64, string, error) {
	rc, err := file.Open()
	if err != nil {
		return 0, "", fmt.Errorf("open archive entry %s: %w", file.Name, err)
	}
	defer rc.Close()
	if file.UncompressedSize64 > uint64(backupEntryMaxBytes) {
		return 0, "", fmt.Errorf("archive entry %s exceeds %d bytes", file.Name, backupEntryMaxBytes)
	}
	reader := io.Reader(rc)
	reader = io.LimitReader(reader, backupEntryMaxBytes+1)

	digest := sha256.New()
	size, err := io.Copy(digest, reader)
	if err != nil {
		return 0, "", fmt.Errorf("read archive entry %s: %w", file.Name, err)
	}
	if size > backupEntryMaxBytes {
		return 0, "", fmt.Errorf("archive entry %s exceeds %d bytes", file.Name, backupEntryMaxBytes)
	}
	return size, hex.EncodeToString(digest.Sum(nil)), nil
}

func readZipFile(file *zip.File, maxBytes int64) ([]byte, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("open archive entry %s: %w", file.Name, err)
	}
	defer rc.Close()
	if maxBytes > 0 && file.UncompressedSize64 > uint64(maxBytes) {
		return nil, fmt.Errorf("archive entry %s exceeds %d bytes", file.Name, maxBytes)
	}
	reader := io.Reader(rc)
	if maxBytes > 0 {
		reader = io.LimitReader(rc, maxBytes+1)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read archive entry %s: %w", file.Name, err)
	}
	if maxBytes > 0 && int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("archive entry %s exceeds %d bytes", file.Name, maxBytes)
	}
	return raw, nil
}

func normalizeChecksum(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if len(value) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return value
}

func backupAuditPath(cfg *config.Config) string {
	if cfg != nil {
		for _, output := range cfg.Audit.Outputs {
			if strings.EqualFold(strings.TrimSpace(output.Type), "file") && strings.TrimSpace(output.Path) != "" {
				return strings.TrimSpace(output.Path)
			}
		}
	}
	if cfg == nil {
		return filepath.Join(".", "data", "audit.jsonl")
	}
	return filepath.Join(cfg.Server.DataDir, "audit.jsonl")
}

func defaultBackupArchiveName() string {
	return fmt.Sprintf("kervan-backup-%s.zip", time.Now().UTC().Format("20060102-150405"))
}

// snapshotSQLiteStore takes a consistent VACUUM INTO snapshot of the SQLite
// store in dataDir into a temporary directory, for archiving. Opening the
// store first also imports a kervan-store.json that has not been imported
// yet, so the backup holds the data the server will use.
func snapshotSQLiteStore(dataDir string) (string, func(), error) {
	st, err := store.OpenWithOptions(dataDir, store.Options{Backend: store.BackendSQLite})
	if err != nil {
		return "", nil, fmt.Errorf("open store: %w", err)
	}
	defer st.Close()
	tmpDir, err := os.MkdirTemp("", "kervan-backup-store-*")
	if err != nil {
		return "", nil, fmt.Errorf("create snapshot directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	snapshot := filepath.Join(tmpDir, store.FileName(store.BackendSQLite))
	if err := st.Snapshot(snapshot); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("snapshot store: %w", err)
	}
	return snapshot, cleanup, nil
}

// prepareSQLiteRestore writes the database to restore into a temporary
// directory inside dataDir (so the final move is a rename on the same file
// system) and verifies it. fromDB selects the archived database; otherwise
// the archived JSON store is imported into a new database.
func prepareSQLiteRestore(entries map[string]*zip.File, dataDir string, fromDB bool) (string, func(), error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return "", nil, fmt.Errorf("create data directory %s: %w", dataDir, err)
	}
	tmpDir, err := os.MkdirTemp(dataDir, ".kervan-restore-*")
	if err != nil {
		return "", nil, fmt.Errorf("create restore directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	prepared := filepath.Join(tmpDir, store.FileName(store.BackendSQLite))
	if fromDB {
		if err := restoreBackupFile(entries[backupStoreDBArchivePath], prepared, false); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("extract %s: %w", backupStoreDBArchivePath, err)
		}
	} else {
		jsonCopy := filepath.Join(tmpDir, "kervan-store.json")
		if err := restoreBackupFile(entries[backupStoreArchivePath], jsonCopy, false); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("extract %s: %w", backupStoreArchivePath, err)
		}
		if err := store.BuildSQLiteFromJSON(jsonCopy, prepared); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("import %s into an SQLite store: %w", backupStoreArchivePath, err)
		}
	}
	if err := store.VerifySQLiteFile(prepared); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("verify restored store: %w", err)
	}
	return prepared, cleanup, nil
}

// verifyArchivedJSONStore extracts the archived store files to a temporary
// directory and checks that a store can actually be OPENED from them: the
// primary, or the .bak the JSON backend falls back to.
//
// Checksums alone cannot say that. An archive whose store files are unreadable
// passes the manifest, restores cleanly, and then the installation refuses to
// start — which is the same failure verifyArchivedSQLiteStore exists to prevent
// on the SQLite side.
func verifyArchivedJSONStore(entries map[string]*zip.File) error {
	tmpDir, err := os.MkdirTemp("", "kervan-verify-store-*")
	if err != nil {
		return fmt.Errorf("create verify directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	for _, f := range []struct{ archivePath, name string }{
		{backupStoreArchivePath, "kervan-store.json"},
		{backupStoreBakArchivePath, "kervan-store.json.bak"},
	} {
		entry, ok := entries[f.archivePath]
		if !ok {
			continue
		}
		if err := restoreBackupFile(entry, filepath.Join(tmpDir, f.name), false); err != nil {
			return fmt.Errorf("extract %s: %w", f.archivePath, err)
		}
	}
	st, err := store.OpenWithOptions(tmpDir, store.Options{Backend: store.BackendJSON})
	if err != nil {
		return fmt.Errorf("verify %s: %w", backupStoreArchivePath, err)
	}
	return st.Close()
}

// verifyArchivedSQLiteStore extracts the archived database to a temporary
// file and checks it with SQLite's integrity check.
func verifyArchivedSQLiteStore(entry *zip.File) error {
	tmpDir, err := os.MkdirTemp("", "kervan-verify-store-*")
	if err != nil {
		return fmt.Errorf("create verify directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	path := filepath.Join(tmpDir, store.FileName(store.BackendSQLite))
	if err := restoreBackupFile(entry, path, false); err != nil {
		return fmt.Errorf("extract %s: %w", backupStoreDBArchivePath, err)
	}
	if err := store.VerifySQLiteFile(path); err != nil {
		return fmt.Errorf("verify %s: %w", backupStoreDBArchivePath, err)
	}
	return nil
}
