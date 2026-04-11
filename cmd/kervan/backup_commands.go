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
)

const (
	backupStoreArchivePath    = "store/kervan-store.json"
	backupStoreBakArchivePath = "store/kervan-store.json.bak"
	backupAuditArchivePath    = "audit/audit.jsonl"
	backupConfigArchivePath   = "config/kervan.yaml"
	backupManifestPath        = "manifest.json"
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
		return err
	}
	if strings.TrimSpace(*outputPath) == "" {
		return errors.New("--output is required")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
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

	if err := os.MkdirAll(filepath.Dir(*outputPath), 0o755); err != nil {
		return err
	}

	out, err := os.OpenFile(*outputPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()

	zipWriter := zip.NewWriter(out)
	manifest := backupManifest{
		Version:     2,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		ConfigPath:  *configPath,
		DataDir:     cfg.Server.DataDir,
	}
	included := make([]string, 0, len(files))

	for _, file := range files {
		raw, err := os.ReadFile(file.SourcePath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && !file.Required {
				continue
			}
			return fmt.Errorf("read %s: %w", file.SourcePath, err)
		}
		entry, err := zipWriter.Create(file.ArchivePath)
		if err != nil {
			return err
		}
		if _, err := entry.Write(raw); err != nil {
			return err
		}
		included = append(included, file.ArchivePath)
		manifest.Files = append(manifest.Files, backupManifestEntry{
			ArchivePath: file.ArchivePath,
			SourcePath:  file.SourcePath,
			Size:        int64(len(raw)),
			SHA256:      checksumSHA256(raw),
		})
	}

	manifestRaw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	manifestEntry, err := zipWriter.Create(backupManifestPath)
	if err != nil {
		return err
	}
	if _, err := manifestEntry.Write(manifestRaw); err != nil {
		return err
	}
	if err := zipWriter.Close(); err != nil {
		return err
	}

	payload := map[string]any{
		"created":        true,
		"output":         *outputPath,
		"included_files": included,
	}
	if *jsonOut {
		return json.NewEncoder(stdout).Encode(payload)
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
		return err
	}
	if strings.TrimSpace(*inputPath) == "" {
		return errors.New("--input is required")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	reader, err := zip.OpenReader(*inputPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	archiveEntries, manifest, err := loadBackupArchive(reader.File)
	if err != nil {
		return err
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
			return err
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
	}
	if manifest != nil {
		payload["verified"] = manifest.Version >= 2
	}
	if *jsonOut {
		return json.NewEncoder(stdout).Encode(payload)
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
		return err
	}
	if strings.TrimSpace(*inputPath) == "" {
		return errors.New("--input is required")
	}

	reader, err := zip.OpenReader(*inputPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	archiveEntries, manifest, err := loadBackupArchive(reader.File)
	if err != nil {
		return err
	}
	if _, ok := archiveEntries[backupStoreArchivePath]; !ok {
		return fmt.Errorf("backup archive missing required entry %s", backupStoreArchivePath)
	}

	entryCount := len(archiveEntries)
	payload := map[string]any{
		"verified":         true,
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
		return json.NewEncoder(stdout).Encode(payload)
	}

	_, _ = fmt.Fprintf(stdout, "Backup verified: %s\n", *inputPath)
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
		raw, err := readZipFile(file)
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
			return nil, nil, err
		}
	}

	return entries, manifest, nil
}

func restoreBackupFile(file *zip.File, targetPath string, force bool) error {
	if !force {
		if _, err := os.Stat(targetPath); err == nil {
			return fmt.Errorf("refusing to overwrite existing file %s without --force", targetPath)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	rc, err := file.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	raw, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(targetPath, raw, 0o600)
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
		raw, err := readZipFile(entry)
		if err != nil {
			return fmt.Errorf("read %s: %w", item.ArchivePath, err)
		}
		if item.Size != int64(len(raw)) {
			return fmt.Errorf("backup integrity check failed for %s: size mismatch", item.ArchivePath)
		}
		if want := normalizeChecksum(item.SHA256); want != "" {
			if got := checksumSHA256(raw); got != want {
				return fmt.Errorf("backup integrity check failed for %s: checksum mismatch", item.ArchivePath)
			}
		}
	}
	return nil
}

func readZipFile(file *zip.File) ([]byte, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func checksumSHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
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
