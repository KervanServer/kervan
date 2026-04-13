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

	"github.com/kervanserver/kervan/internal/config"
	"github.com/kervanserver/kervan/internal/store"
)

const (
	backupStoreArchivePath    = "store/kervan-store.json"
	backupStoreBakArchivePath = "store/kervan-store.json.bak"
	backupAuditArchivePath    = "audit/audit.jsonl"
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

	files := []backupArchiveFile{
		{
			ArchivePath: backupStoreArchivePath,
			SourcePath:  filepath.Join(cfg.Server.DataDir, "kervan-store.json"),
			Required:    true,
		},
		{
			ArchivePath: backupStoreBakArchivePath,
			SourcePath:  filepath.Join(cfg.Server.DataDir, "kervan-store.json.bak"),
		},
	}
	if *includeAudit {
		files = append(files, backupArchiveFile{
			ArchivePath: backupAuditArchivePath,
			SourcePath:  backupAuditPath(cfg),
		})
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
		manifest.Files = append(manifest.Files, backupManifestEntry{
			ArchivePath: file.ArchivePath,
			SourcePath:  file.SourcePath,
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

	targets := map[string]backupRestoreEntry{
		backupStoreArchivePath: {
			ArchivePath: backupStoreArchivePath,
			TargetPath:  filepath.Join(cfg.Server.DataDir, "kervan-store.json"),
		},
		backupStoreBakArchivePath: {
			ArchivePath: backupStoreBakArchivePath,
			TargetPath:  filepath.Join(cfg.Server.DataDir, "kervan-store.json.bak"),
			Optional:    true,
		},
		backupAuditArchivePath: {
			ArchivePath: backupAuditArchivePath,
			TargetPath:  backupAuditPath(cfg),
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

	restored := make([]string, 0, len(targets))
	seen := make(map[string]bool, len(targets))
	for archivePath, target := range targets {
		file, ok := archiveEntries[archivePath]
		if !ok {
			continue
		}
		seen[archivePath] = true
		if err := restoreBackupFile(file, target.TargetPath, *force); err != nil {
			return fmt.Errorf("restore %s to %s: %w", archivePath, target.TargetPath, err)
		}
		restored = append(restored, archivePath)
	}

	if !seen[backupStoreArchivePath] {
		return fmt.Errorf("backup archive missing required entry %s", backupStoreArchivePath)
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
	if _, ok := archiveEntries[backupStoreArchivePath]; !ok {
		return fmt.Errorf("backup archive missing required entry %s", backupStoreArchivePath)
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
