package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/kervanserver/kervan/internal/auth"
	"golang.org/x/crypto/ssh"
)

func cmdMigrate(args []string) {
	if err := runMigrateCommand(os.Stdout, args); err != nil {
		exitf("migrate: %v", err)
	}
}

func runMigrateCommand(stdout io.Writer, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: kervan migrate <vsftpd|proftpd> [flags]")
	}
	switch args[0] {
	case "vsftpd":
		return runMigrateVSFTPDCommand(stdout, args[1:])
	case "proftpd":
		return runMigrateProFTPDCommand(stdout, args[1:])
	case "ssh-keys":
		return runMigrateSSHKeysCommand(stdout, args[1:])
	default:
		return fmt.Errorf("unknown migrate command: %s", args[0])
	}
}

func runMigrateVSFTPDCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("migrate vsftpd", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	userDBPath := fs.String("user-db", "", "Path to vsftpd plain-text virtual users file")
	homeRoot := fs.String("home-root", "/", "Base home directory prefix for imported users")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*userDBPath) == "" {
		return errors.New("--user-db is required")
	}

	entries, err := parseVSFTPDUserDB(*userDBPath)
	if err != nil {
		return err
	}

	ctx, err := openCLIContext(*configPath)
	if err != nil {
		return err
	}
	defer ctx.close()

	report := migrationReport{
		Source: *userDBPath,
		Kind:   "vsftpd",
		Total:  len(entries),
	}
	for _, entry := range entries {
		homeDir := path.Join("/", strings.TrimSpace(*homeRoot), entry.Username)
		user, createErr := ctx.engine.CreateUser(entry.Username, entry.Password, homeDir, false)
		if createErr != nil {
			report.Skipped++
			report.Errors = append(report.Errors, migrationError{
				Username: entry.Username,
				Error:    createErr.Error(),
			})
			continue
		}
		report.Migrated++
		report.Users = append(report.Users, user.Username)
	}

	if *jsonOut {
		return json.NewEncoder(stdout).Encode(report)
	}

	_, _ = fmt.Fprintf(stdout, "Migrated vsftpd users from %s\n", *userDBPath)
	_, _ = fmt.Fprintf(stdout, "Migrated: %d\n", report.Migrated)
	_, _ = fmt.Fprintf(stdout, "Skipped: %d\n", report.Skipped)
	if len(report.Errors) > 0 {
		_, _ = fmt.Fprintln(stdout, "Errors:")
		for _, item := range report.Errors {
			_, _ = fmt.Fprintf(stdout, "  %s: %s\n", item.Username, item.Error)
		}
	}
	return nil
}

type vsftpdUserEntry struct {
	Username string
	Password string
}

type migrationError struct {
	Username string `json:"username"`
	Error    string `json:"error"`
}

type migrationReport struct {
	Source    string           `json:"source"`
	Kind      string           `json:"kind"`
	Total     int              `json:"total"`
	Migrated  int              `json:"migrated"`
	Skipped   int              `json:"skipped"`
	Users     []string         `json:"users,omitempty"`
	KeyCounts map[string]int   `json:"key_counts,omitempty"`
	Errors    []migrationError `json:"errors,omitempty"`
	Warnings  []string         `json:"warnings,omitempty"`
}

func parseVSFTPDUserDB(filePath string) ([]vsftpdUserEntry, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	lines := make([]string, 0, 16)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, errors.New("vsftpd user db is empty")
	}
	if len(lines)%2 != 0 {
		return nil, errors.New("vsftpd user db must contain alternating username/password lines")
	}

	entries := make([]vsftpdUserEntry, 0, len(lines)/2)
	for i := 0; i < len(lines); i += 2 {
		username := strings.TrimSpace(lines[i])
		password := lines[i+1]
		if username == "" {
			return nil, fmt.Errorf("entry %d: username is empty", i/2+1)
		}
		if password == "" {
			return nil, fmt.Errorf("entry %d (%s): password is empty", i/2+1, username)
		}
		entries = append(entries, vsftpdUserEntry{
			Username: username,
			Password: password,
		})
	}
	return entries, nil
}

func runMigrateProFTPDCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("migrate proftpd", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	kervanConfigPath := fs.String("kervan-config", defaultConfigPath, "Path to Kervan config file")
	proftpdConfigPath := fs.String("config", "", "Path to ProFTPD config file")
	proftpdConfigPathAlias := fs.String("source-config", "", "Path to ProFTPD config file")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sourceConfigPath := strings.TrimSpace(*proftpdConfigPath)
	if sourceConfigPath == "" {
		sourceConfigPath = strings.TrimSpace(*proftpdConfigPathAlias)
	}
	if sourceConfigPath == "" {
		return errors.New("--config is required")
	}

	source, err := parseProFTPDConfig(sourceConfigPath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(source.AuthUserFile) == "" {
		return errors.New("AuthUserFile directive not found in ProFTPD config")
	}

	entries, warnings, err := parseProFTPDUserFile(source.AuthUserFile)
	if err != nil {
		return err
	}

	ctx, err := openCLIContext(*kervanConfigPath)
	if err != nil {
		return err
	}
	defer ctx.close()

	report := migrationReport{
		Source:   sourceConfigPath,
		Kind:     "proftpd",
		Total:    len(entries),
		Warnings: append([]string{}, source.UnsupportedDirectives...),
	}
	report.Warnings = append(report.Warnings, warnings...)

	for _, entry := range entries {
		user, createErr := createMigratedProFTPDUser(ctx, entry)
		if createErr != nil {
			report.Skipped++
			report.Errors = append(report.Errors, migrationError{
				Username: entry.Username,
				Error:    createErr.Error(),
			})
			continue
		}
		report.Migrated++
		report.Users = append(report.Users, user.Username)
	}

	if *jsonOut {
		return json.NewEncoder(stdout).Encode(report)
	}

	_, _ = fmt.Fprintf(stdout, "Migrated ProFTPD users from %s\n", source.AuthUserFile)
	_, _ = fmt.Fprintf(stdout, "Migrated: %d\n", report.Migrated)
	_, _ = fmt.Fprintf(stdout, "Skipped: %d\n", report.Skipped)
	if len(report.Warnings) > 0 {
		_, _ = fmt.Fprintln(stdout, "Warnings:")
		for _, warning := range report.Warnings {
			_, _ = fmt.Fprintf(stdout, "  %s\n", warning)
		}
	}
	if len(report.Errors) > 0 {
		_, _ = fmt.Fprintln(stdout, "Errors:")
		for _, item := range report.Errors {
			_, _ = fmt.Fprintf(stdout, "  %s: %s\n", item.Username, item.Error)
		}
	}
	return nil
}

type proFTPDConfig struct {
	AuthUserFile          string
	UnsupportedDirectives []string
}

type proFTPDUserEntry struct {
	Username     string
	Password     string
	PasswordHash string
	HomeDir      string
	Disabled     bool
}

func parseProFTPDConfig(filePath string) (*proFTPDConfig, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	cfg := &proFTPDConfig{}
	baseDir := filepath.Dir(filePath)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		directive := strings.ToLower(fields[0])
		value := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, fields[0])), `"`)
		switch directive {
		case "authuserfile":
			cfg.AuthUserFile = resolveProFTPDPath(baseDir, value)
		case "authgroupfile", "defaultroot", "requirevalidshell", "authorder", "<limit", "</limit", "allowuser", "denyuser", "umask":
			cfg.UnsupportedDirectives = append(cfg.UnsupportedDirectives, strings.TrimSpace(line))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func parseProFTPDUserFile(filePath string) ([]proFTPDUserEntry, []string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()

	entries := make([]proFTPDUserEntry, 0, 16)
	warnings := make([]string, 0)
	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) < 7 {
			return nil, warnings, fmt.Errorf("invalid AuthUserFile entry on line %d", lineNo)
		}
		username := strings.TrimSpace(parts[0])
		passwordField := strings.TrimSpace(parts[1])
		homeDir := strings.TrimSpace(parts[5])
		shell := strings.TrimSpace(parts[6])
		if username == "" {
			return nil, warnings, fmt.Errorf("line %d: username is empty", lineNo)
		}
		if homeDir == "" {
			homeDir = "/"
		}

		entry := proFTPDUserEntry{
			Username: username,
			HomeDir:  homeDir,
			Disabled: shell == "/bin/false" || shell == "/sbin/nologin" || shell == "/usr/sbin/nologin",
		}
		switch {
		case passwordField == "":
			return nil, warnings, fmt.Errorf("line %d (%s): password is empty", lineNo, username)
		case strings.HasPrefix(passwordField, "$2a$"), strings.HasPrefix(passwordField, "$2b$"), strings.HasPrefix(passwordField, "$2y$"):
			entry.PasswordHash = passwordField
		case strings.HasPrefix(passwordField, "$"):
			warnings = append(warnings, fmt.Sprintf("unsupported password hash for user %s", username))
			entry.PasswordHash = ""
		default:
			entry.Password = passwordField
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, warnings, err
	}
	return entries, warnings, nil
}

func createMigratedProFTPDUser(ctx *cliContext, entry proFTPDUserEntry) (*auth.User, error) {
	switch {
	case entry.PasswordHash != "":
		user := &auth.User{
			Username:     entry.Username,
			PasswordHash: entry.PasswordHash,
			Type:         auth.UserTypeVirtual,
			HomeDir:      entry.HomeDir,
			Enabled:      !entry.Disabled,
			Permissions:  auth.DefaultUserPermissions(),
		}
		if err := ctx.repo.Create(user); err != nil {
			return nil, err
		}
		return user, nil
	case entry.Password != "":
		user, err := ctx.engine.CreateUser(entry.Username, entry.Password, entry.HomeDir, false)
		if err != nil {
			return nil, err
		}
		if entry.Disabled {
			user.Enabled = false
			if err := ctx.repo.Update(user); err != nil {
				return nil, err
			}
		}
		return user, nil
	default:
		return nil, errors.New("password format is not supported")
	}
}

func resolveProFTPDPath(baseDir, raw string) string {
	trimmed := strings.Trim(strings.TrimSpace(raw), `"`)
	if trimmed == "" {
		return ""
	}
	if filepath.IsAbs(trimmed) {
		return trimmed
	}
	return filepath.Join(baseDir, trimmed)
}

func runMigrateSSHKeysCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("migrate ssh-keys", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to Kervan config file")
	authorizedKeysPattern := fs.String("authorized-keys-dir", "", "Glob to .ssh directories or authorized_keys files")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*authorizedKeysPattern) == "" {
		return errors.New("--authorized-keys-dir is required")
	}

	matches, err := filepath.Glob(*authorizedKeysPattern)
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return errors.New("no authorized_keys paths matched")
	}

	ctx, err := openCLIContext(*configPath)
	if err != nil {
		return err
	}
	defer ctx.close()

	report := migrationReport{
		Source:    *authorizedKeysPattern,
		Kind:      "ssh-keys",
		KeyCounts: map[string]int{},
	}
	seenUsers := map[string]struct{}{}

	for _, match := range matches {
		targetFile, username, homeDir, resolveErr := resolveAuthorizedKeysTarget(match)
		if resolveErr != nil {
			report.Skipped++
			report.Errors = append(report.Errors, migrationError{
				Username: username,
				Error:    resolveErr.Error(),
			})
			continue
		}
		keys, warnings, parseErr := parseAuthorizedKeysFile(targetFile)
		report.Warnings = append(report.Warnings, warnings...)
		if parseErr != nil {
			report.Skipped++
			report.Errors = append(report.Errors, migrationError{
				Username: username,
				Error:    parseErr.Error(),
			})
			continue
		}
		report.Total++
		if len(keys) == 0 {
			report.Skipped++
			report.Warnings = append(report.Warnings, fmt.Sprintf("no valid public keys found for %s", username))
			continue
		}
		user, importedCount, updateErr := createOrUpdateAuthorizedKeyUser(ctx, username, homeDir, keys)
		if updateErr != nil {
			report.Skipped++
			report.Errors = append(report.Errors, migrationError{
				Username: username,
				Error:    updateErr.Error(),
			})
			continue
		}
		report.KeyCounts[user.Username] += importedCount
		if _, exists := seenUsers[user.Username]; !exists {
			seenUsers[user.Username] = struct{}{}
			report.Users = append(report.Users, user.Username)
		}
		report.Migrated++
	}

	if *jsonOut {
		return json.NewEncoder(stdout).Encode(report)
	}

	_, _ = fmt.Fprintf(stdout, "Migrated SSH authorized_keys from %s\n", *authorizedKeysPattern)
	_, _ = fmt.Fprintf(stdout, "Accounts processed: %d\n", report.Migrated)
	_, _ = fmt.Fprintf(stdout, "Skipped: %d\n", report.Skipped)
	if len(report.KeyCounts) > 0 {
		_, _ = fmt.Fprintln(stdout, "Imported keys:")
		for _, username := range report.Users {
			_, _ = fmt.Fprintf(stdout, "  %s: %d\n", username, report.KeyCounts[username])
		}
	}
	if len(report.Warnings) > 0 {
		_, _ = fmt.Fprintln(stdout, "Warnings:")
		for _, warning := range report.Warnings {
			_, _ = fmt.Fprintf(stdout, "  %s\n", warning)
		}
	}
	if len(report.Errors) > 0 {
		_, _ = fmt.Fprintln(stdout, "Errors:")
		for _, item := range report.Errors {
			_, _ = fmt.Fprintf(stdout, "  %s: %s\n", item.Username, item.Error)
		}
	}
	return nil
}

func resolveAuthorizedKeysTarget(match string) (targetFile, username, homeDir string, err error) {
	info, err := os.Stat(match)
	if err != nil {
		return "", "", "", err
	}
	if info.IsDir() {
		targetFile = filepath.Join(match, "authorized_keys")
	} else {
		targetFile = match
	}
	if filepath.Base(targetFile) != "authorized_keys" {
		return "", "", "", fmt.Errorf("path %q does not resolve to an authorized_keys file", match)
	}

	sshDir := filepath.Dir(targetFile)
	usernameDir := filepath.Dir(sshDir)
	username = filepath.Base(usernameDir)
	if username == "." || username == string(filepath.Separator) || username == "" {
		return "", "", "", fmt.Errorf("unable to infer username from %q", match)
	}
	return targetFile, username, usernameDir, nil
}

func parseAuthorizedKeysFile(filePath string) ([]string, []string, error) {
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return nil, nil, err
	}
	keys := make([]string, 0, 4)
	warnings := make([]string, 0)
	remaining := raw
	for len(remaining) > 0 {
		pubKey, _, _, rest, parseErr := ssh.ParseAuthorizedKey(remaining)
		if parseErr != nil {
			line, next := splitAuthorizedKeyChunk(remaining)
			remaining = next
			trimmed := strings.TrimSpace(string(line))
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			warnings = append(warnings, fmt.Sprintf("skipped invalid authorized_keys line in %s", filePath))
			continue
		}
		keys = append(keys, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pubKey))))
		remaining = rest
	}
	return uniqueStrings(keys), warnings, nil
}

func splitAuthorizedKeyChunk(raw []byte) ([]byte, []byte) {
	for idx, b := range raw {
		if b == '\n' {
			return raw[:idx], raw[idx+1:]
		}
	}
	return raw, nil
}

func createOrUpdateAuthorizedKeyUser(ctx *cliContext, username, homeDir string, keys []string) (*auth.User, int, error) {
	user, err := ctx.repo.GetByUsername(username)
	if err != nil {
		return nil, 0, err
	}
	if user == nil {
		user = &auth.User{
			Username:       username,
			AuthorizedKeys: uniqueStrings(keys),
			HomeDir:        homeDir,
			Enabled:        true,
			Type:           auth.UserTypeVirtual,
			Permissions:    auth.DefaultUserPermissions(),
		}
		if err := ctx.repo.Create(user); err != nil {
			return nil, 0, err
		}
		return user, len(user.AuthorizedKeys), nil
	}

	before := len(user.AuthorizedKeys)
	user.AuthorizedKeys = uniqueStrings(append(user.AuthorizedKeys, keys...))
	if strings.TrimSpace(user.HomeDir) == "" || user.HomeDir == "/" {
		user.HomeDir = homeDir
	}
	if err := ctx.repo.Update(user); err != nil {
		return nil, 0, err
	}
	return user, len(user.AuthorizedKeys) - before, nil
}

func uniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, item := range in {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
