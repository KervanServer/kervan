package main

import (
	"crypto/tls"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	iapi "github.com/kervanserver/kervan/internal/api"
	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/config"
	"github.com/kervanserver/kervan/internal/store"
)

func cmdCheck(args []string) {
	if err := runCheckCommand(os.Stdout, args); err != nil {
		exitf("check config: %v", err)
	}
}

func cmdStatus(args []string) {
	if err := runStatusCommand(os.Stdout, args); err != nil {
		exitf("status: %v", err)
	}
}

func cmdUser(args []string) {
	if err := runUserCommand(os.Stdout, args); err != nil {
		exitf("user: %v", err)
	}
}

func runAPIKeyCommand(stdout io.Writer, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: kervan apikey <list|create|revoke|scopes|presets> [flags]")
	}
	switch args[0] {
	case "list":
		return runAPIKeyListCommand(stdout, args[1:])
	case "create":
		return runAPIKeyCreateCommand(stdout, args[1:])
	case "revoke":
		return runAPIKeyRevokeCommand(stdout, args[1:])
	case "scopes":
		return runAPIKeyScopesCommand(stdout, args[1:])
	case "presets":
		return runAPIKeyPresetsCommand(stdout, args[1:])
	default:
		return fmt.Errorf("unknown apikey command: %s", args[0])
	}
}

func runCheckCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	payload := map[string]any{
		"valid":         true,
		"config_path":   *configPath,
		"data_dir":      cfg.Server.DataDir,
		"ftp_enabled":   cfg.FTP.Enabled,
		"sftp_enabled":  cfg.SFTP.Enabled,
		"scp_enabled":   cfg.SCP.Enabled,
		"webui_enabled": cfg.WebUI.Enabled,
	}
	if *jsonOut {
		return json.NewEncoder(stdout).Encode(payload)
	}

	_, _ = fmt.Fprintf(stdout, "Config valid: %s\n", *configPath)
	_, _ = fmt.Fprintf(stdout, "Data directory: %s\n", cfg.Server.DataDir)
	_, _ = fmt.Fprintf(stdout, "Services: ftp=%t sftp=%t scp=%t webui=%t\n", cfg.FTP.Enabled, cfg.SFTP.Enabled, cfg.SCP.Enabled, cfg.WebUI.Enabled)
	return nil
}

func runStatusCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	timeout := fs.Duration("timeout", 5*time.Second, "Request timeout")
	insecure := fs.Bool("insecure", false, "Skip TLS certificate verification")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if !cfg.WebUI.Enabled {
		return errors.New("webui/api is disabled in config")
	}

	address := net.JoinHostPort(statusHost(cfg.WebUI.BindAddress), fmt.Sprintf("%d", cfg.WebUI.Port))
	scheme := "http"
	client := &http.Client{Timeout: *timeout}
	if cfg.WebUI.TLS {
		scheme = "https"
		if *insecure {
			client.Transport = &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			}
		}
	}

	resp, err := client.Get(scheme + "://" + address + "/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("health endpoint returned %s", resp.Status)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}

	if *jsonOut {
		return json.NewEncoder(stdout).Encode(payload)
	}

	_, _ = fmt.Fprintf(stdout, "Server status: %v\n", payload["status"])
	if version, ok := payload["version"]; ok {
		_, _ = fmt.Fprintf(stdout, "Version: %v\n", version)
	}
	if uptime, ok := payload["uptime_seconds"]; ok {
		_, _ = fmt.Fprintf(stdout, "Uptime (sec): %v\n", uptime)
	}

	checks, _ := payload["checks"].(map[string]any)
	if len(checks) > 0 {
		keys := make([]string, 0, len(checks))
		for key := range checks {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		_, _ = fmt.Fprintln(stdout, "Checks:")
		for _, key := range keys {
			status := "unknown"
			if checkMap, ok := checks[key].(map[string]any); ok {
				if rawStatus, exists := checkMap["status"]; exists {
					status = fmt.Sprint(rawStatus)
				}
			}
			_, _ = fmt.Fprintf(stdout, "  %s: %s\n", key, status)
		}
	}
	return nil
}

func runUserCommand(stdout io.Writer, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: kervan user <list|create|delete|import|export> [flags]")
	}
	switch args[0] {
	case "list":
		return runUserListCommand(stdout, args[1:])
	case "create":
		return runUserCreateCommand(stdout, args[1:])
	case "delete":
		return runUserDeleteCommand(stdout, args[1:])
	case "import":
		return runUserImportCommand(stdout, args[1:])
	case "export":
		return runUserExportCommand(stdout, args[1:])
	default:
		return fmt.Errorf("unknown user command: %s", args[0])
	}
}

func runAPIKeyScopesCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("apikey scopes", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	scopes := iapi.SupportedAPIKeyScopes()
	if *jsonOut {
		return json.NewEncoder(stdout).Encode(scopes)
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SCOPE\tRESOURCE\tACCESS\tDESCRIPTION")
	for _, scope := range scopes {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", scope.Name, scope.Resource, scope.Access, scope.Description)
	}
	return tw.Flush()
}

func runAPIKeyPresetsCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("apikey presets", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	presets := iapi.APIKeyPresets()
	if *jsonOut {
		return json.NewEncoder(stdout).Encode(presets)
	}

	for _, preset := range presets {
		_, _ = fmt.Fprintf(stdout, "%s (%s)\n", preset.Label, preset.ID)
		_, _ = fmt.Fprintf(stdout, "  %s\n", preset.Description)
		_, _ = fmt.Fprintf(stdout, "  scopes: %s\n", strings.Join(preset.Scopes, ", "))
	}
	return nil
}

func runAPIKeyListCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("apikey list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	username := fs.String("username", "", "Username")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*username) == "" {
		return errors.New("--username is required")
	}

	ctx, user, err := openCLIAPIKeyContext(*configPath, *username)
	if err != nil {
		return err
	}
	defer ctx.close()

	keys, err := ctx.apiKeys.ListByUser(user.ID)
	if err != nil {
		return err
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i] == nil {
			return false
		}
		if keys[j] == nil {
			return true
		}
		return keys[i].CreatedAt.After(keys[j].CreatedAt)
	})

	if *jsonOut {
		return json.NewEncoder(stdout).Encode(keys)
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tNAME\tPERMISSIONS\tPREFIX\tCREATED\tLAST_USED")
	for _, key := range keys {
		if key == nil {
			continue
		}
		lastUsed := "never"
		if key.LastUsedAt != nil {
			lastUsed = key.LastUsedAt.Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\t%s\t%s\n",
			key.ID,
			key.Name,
			key.Permissions,
			key.Prefix,
			key.CreatedAt.Format(time.RFC3339),
			lastUsed,
		)
	}
	return tw.Flush()
}

func runAPIKeyCreateCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("apikey create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	username := fs.String("username", "", "Username")
	name := fs.String("name", "", "Key name")
	permissions := fs.String("permissions", "read-write", "Preset or comma-separated scope list")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*username) == "" {
		return errors.New("--username is required")
	}
	if strings.TrimSpace(*name) == "" {
		return errors.New("--name is required")
	}

	ctx, user, err := openCLIAPIKeyContext(*configPath, *username)
	if err != nil {
		return err
	}
	defer ctx.close()

	token, record, err := ctx.apiKeys.Create(user.ID, *name, *permissions)
	if err != nil {
		return err
	}

	payload := map[string]any{
		"id":          record.ID,
		"username":    user.Username,
		"name":        record.Name,
		"permissions": record.Permissions,
		"prefix":      record.Prefix,
		"created_at":  record.CreatedAt,
		"key":         token,
	}
	if *jsonOut {
		return json.NewEncoder(stdout).Encode(payload)
	}

	_, _ = fmt.Fprintf(stdout, "API key created for %s\n", user.Username)
	_, _ = fmt.Fprintf(stdout, "ID: %s\n", record.ID)
	_, _ = fmt.Fprintf(stdout, "Permissions: %s\n", record.Permissions)
	_, _ = fmt.Fprintf(stdout, "Key: %s\n", token)
	return nil
}

func runAPIKeyRevokeCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("apikey revoke", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	username := fs.String("username", "", "Username")
	id := fs.String("id", "", "API key ID")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*username) == "" {
		return errors.New("--username is required")
	}
	if strings.TrimSpace(*id) == "" {
		return errors.New("--id is required")
	}

	ctx, user, err := openCLIAPIKeyContext(*configPath, *username)
	if err != nil {
		return err
	}
	defer ctx.close()

	if err := ctx.apiKeys.Delete(user.ID, strings.TrimSpace(*id)); err != nil {
		return err
	}

	payload := map[string]any{
		"revoked":  true,
		"id":       strings.TrimSpace(*id),
		"username": user.Username,
	}
	if *jsonOut {
		return json.NewEncoder(stdout).Encode(payload)
	}

	_, _ = fmt.Fprintf(stdout, "API key revoked: %s (%s)\n", strings.TrimSpace(*id), user.Username)
	return nil
}

func runUserListCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("user list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, err := openCLIContext(*configPath)
	if err != nil {
		return err
	}
	defer ctx.close()

	users, err := ctx.repo.List()
	if err != nil {
		return err
	}
	sort.Slice(users, func(i, j int) bool {
		if users[i] == nil {
			return false
		}
		if users[j] == nil {
			return true
		}
		return strings.ToLower(users[i].Username) < strings.ToLower(users[j].Username)
	})

	if *jsonOut {
		return json.NewEncoder(stdout).Encode(users)
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "USERNAME\tTYPE\tENABLED\tHOME\tUPDATED")
	for _, user := range users {
		if user == nil {
			continue
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%t\t%s\t%s\n",
			user.Username,
			user.Type,
			user.Enabled,
			user.HomeDir,
			user.UpdatedAt.Format(time.RFC3339),
		)
	}
	return tw.Flush()
}

func runUserCreateCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("user create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	username := fs.String("username", "", "Username")
	password := fs.String("password", "", "Password")
	homeDir := fs.String("home-dir", "/", "Home directory")
	admin := fs.Bool("admin", false, "Create as admin user")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*username) == "" {
		return errors.New("--username is required")
	}
	if *password == "" {
		return errors.New("--password is required")
	}

	ctx, err := openCLIContext(*configPath)
	if err != nil {
		return err
	}
	defer ctx.close()

	user, err := ctx.engine.CreateUser(strings.TrimSpace(*username), *password, *homeDir, *admin)
	if err != nil {
		return err
	}

	if *jsonOut {
		return json.NewEncoder(stdout).Encode(user)
	}

	_, _ = fmt.Fprintf(stdout, "User created: %s (%s)\n", user.Username, user.ID)
	return nil
}

func runUserDeleteCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("user delete", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	username := fs.String("username", "", "Username")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*username) == "" {
		return errors.New("--username is required")
	}

	ctx, err := openCLIContext(*configPath)
	if err != nil {
		return err
	}
	defer ctx.close()

	user, err := ctx.repo.GetByUsername(strings.TrimSpace(*username))
	if err != nil {
		return err
	}
	if user == nil {
		return fmt.Errorf("user not found: %s", *username)
	}
	if err := ctx.repo.Delete(user.ID); err != nil {
		return err
	}

	payload := map[string]any{
		"deleted":  true,
		"username": user.Username,
		"id":       user.ID,
	}
	if *jsonOut {
		return json.NewEncoder(stdout).Encode(payload)
	}

	_, _ = fmt.Fprintf(stdout, "User deleted: %s (%s)\n", user.Username, user.ID)
	return nil
}

func runUserImportCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("user import", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	filePath := fs.String("file", "", "Input file path")
	formatFlag := fs.String("format", "auto", "Import format: auto|json|csv")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*filePath) == "" {
		return errors.New("--file is required")
	}

	format, err := normalizeUserDataFormat(*formatFlag, *filePath)
	if err != nil {
		return err
	}
	records, err := loadUserImportRecords(*filePath, format)
	if err != nil {
		return err
	}

	ctx, err := openCLIContext(*configPath)
	if err != nil {
		return err
	}
	defer ctx.close()

	report := userImportReport{
		File:   *filePath,
		Format: format,
		Total:  len(records),
	}
	for _, record := range records {
		createdUser, createErr := createImportedUser(ctx, record)
		if createErr != nil {
			report.Skipped++
			report.Errors = append(report.Errors, userImportError{
				Row:      record.Row,
				Username: record.Username,
				Error:    createErr.Error(),
			})
			continue
		}
		report.Created++
		report.Usernames = append(report.Usernames, createdUser.Username)
	}

	if *jsonOut {
		return json.NewEncoder(stdout).Encode(report)
	}

	_, _ = fmt.Fprintf(stdout, "Imported users from %s\n", *filePath)
	_, _ = fmt.Fprintf(stdout, "Format: %s\n", format)
	_, _ = fmt.Fprintf(stdout, "Created: %d\n", report.Created)
	_, _ = fmt.Fprintf(stdout, "Skipped: %d\n", report.Skipped)
	if len(report.Errors) > 0 {
		_, _ = fmt.Fprintln(stdout, "Errors:")
		for _, item := range report.Errors {
			if item.Username != "" {
				_, _ = fmt.Fprintf(stdout, "  row %d (%s): %s\n", item.Row, item.Username, item.Error)
				continue
			}
			_, _ = fmt.Fprintf(stdout, "  row %d: %s\n", item.Row, item.Error)
		}
	}
	return nil
}

func runUserExportCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("user export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	formatFlag := fs.String("format", "json", "Export format: json|csv")
	outputPath := fs.String("output", "", "Output file path, or '-' for stdout")
	includePasswordHashes := fs.Bool("include-password-hashes", false, "Include password hashes in export output")
	if err := fs.Parse(args); err != nil {
		return err
	}

	format, err := normalizeUserDataFormat(*formatFlag, *outputPath)
	if err != nil {
		return err
	}

	ctx, err := openCLIContext(*configPath)
	if err != nil {
		return err
	}
	defer ctx.close()

	users, err := ctx.repo.List()
	if err != nil {
		return err
	}
	sort.Slice(users, func(i, j int) bool {
		if users[i] == nil {
			return false
		}
		if users[j] == nil {
			return true
		}
		return strings.ToLower(users[i].Username) < strings.ToLower(users[j].Username)
	})

	records := make([]userExportRecord, 0, len(users))
	for _, user := range users {
		if user == nil {
			continue
		}
		record := userExportRecord{
			Username: user.Username,
			Email:    user.Email,
			Role:     exportRoleForUser(user),
			Type:     string(user.Type),
			HomeDir:  user.HomeDir,
			Enabled:  user.Enabled,
		}
		if *includePasswordHashes {
			record.PasswordHash = user.PasswordHash
		}
		records = append(records, record)
	}

	dest := stdout
	writeToStdout := true
	var outputFile *os.File
	var outputTmpPath string
	if trimmedOutput := strings.TrimSpace(*outputPath); trimmedOutput != "" && trimmedOutput != "-" {
		if err := os.MkdirAll(filepath.Dir(trimmedOutput), 0o755); err != nil {
			return err
		}
		file, err := os.CreateTemp(filepath.Dir(trimmedOutput), filepath.Base(trimmedOutput)+".*.tmp")
		if err != nil {
			return err
		}
		outputFile = file
		outputTmpPath = file.Name()
		defer func() {
			if outputFile != nil {
				_ = outputFile.Close()
			}
			if outputTmpPath != "" {
				_ = os.Remove(outputTmpPath)
			}
		}()
		dest = file
		writeToStdout = false
	}

	switch format {
	case "json":
		encoder := json.NewEncoder(dest)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(records); err != nil {
			return err
		}
	case "csv":
		if err := writeUserExportCSV(dest, records, *includePasswordHashes); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported export format: %s", format)
	}

	if !writeToStdout {
		if err := outputFile.Chmod(0o600); err != nil {
			return err
		}
		if err := outputFile.Sync(); err != nil {
			return err
		}
		if err := outputFile.Close(); err != nil {
			return err
		}
		outputFile = nil
		if err := store.ReplaceTempFileAtomically(outputTmpPath, strings.TrimSpace(*outputPath)); err != nil {
			return err
		}
		outputTmpPath = ""
		_, _ = fmt.Fprintf(stdout, "Exported %d users to %s\n", len(records), strings.TrimSpace(*outputPath))
	}
	return nil
}

type cliContext struct {
	cfg     *config.Config
	store   *store.Store
	repo    *auth.UserRepository
	engine  *auth.Engine
	apiKeys *iapi.APIKeyRepository
}

func openCLIContext(configPath string) (*cliContext, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(cfg.Server.DataDir)
	if err != nil {
		return nil, err
	}
	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, cfg.Auth.PasswordHash, cfg.Security.BruteForce.MaxAttempts, cfg.Security.BruteForce.LockoutDuration)
	engine.SetMinPasswordLength(cfg.Auth.MinPasswordLength)
	return &cliContext{
		cfg:     cfg,
		store:   st,
		repo:    repo,
		engine:  engine,
		apiKeys: iapi.NewAPIKeyRepository(st),
	}, nil
}

func openCLIAPIKeyContext(configPath, username string) (*cliContext, *auth.User, error) {
	ctx, err := openCLIContext(configPath)
	if err != nil {
		return nil, nil, err
	}
	user, err := ctx.repo.GetByUsername(strings.TrimSpace(username))
	if err != nil {
		ctx.close()
		return nil, nil, err
	}
	if user == nil {
		ctx.close()
		return nil, nil, fmt.Errorf("user not found: %s", strings.TrimSpace(username))
	}
	return ctx, user, nil
}

func (c *cliContext) close() {
	if c == nil || c.store == nil {
		return
	}
	_ = c.store.Close()
}

func statusHost(bindAddress string) string {
	host := strings.TrimSpace(bindAddress)
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		return "127.0.0.1"
	default:
		return host
	}
}

type userImportRecord struct {
	Row          int
	Username     string `json:"username"`
	Password     string `json:"password,omitempty"`
	PasswordHash string `json:"password_hash,omitempty"`
	Email        string `json:"email,omitempty"`
	Role         string `json:"role,omitempty"`
	Type         string `json:"type,omitempty"`
	HomeDir      string `json:"home_dir,omitempty"`
	Enabled      *bool  `json:"enabled,omitempty"`
}

type userImportError struct {
	Row      int    `json:"row"`
	Username string `json:"username,omitempty"`
	Error    string `json:"error"`
}

type userImportReport struct {
	File      string            `json:"file"`
	Format    string            `json:"format"`
	Total     int               `json:"total"`
	Created   int               `json:"created"`
	Skipped   int               `json:"skipped"`
	Usernames []string          `json:"usernames,omitempty"`
	Errors    []userImportError `json:"errors,omitempty"`
}

type userExportRecord struct {
	Username     string `json:"username"`
	Email        string `json:"email,omitempty"`
	Role         string `json:"role"`
	Type         string `json:"type"`
	HomeDir      string `json:"home_dir"`
	Enabled      bool   `json:"enabled"`
	PasswordHash string `json:"password_hash,omitempty"`
}

func normalizeUserDataFormat(formatFlag, filePath string) (string, error) {
	format := strings.ToLower(strings.TrimSpace(formatFlag))
	if format == "" || format == "auto" {
		switch strings.ToLower(filepath.Ext(strings.TrimSpace(filePath))) {
		case ".json":
			return "json", nil
		case ".csv":
			return "csv", nil
		default:
			if strings.TrimSpace(filePath) == "" {
				return "json", nil
			}
			return "", fmt.Errorf("unable to detect format for %q; use --format json or --format csv", filePath)
		}
	}
	switch format {
	case "json", "csv":
		return format, nil
	default:
		return "", fmt.Errorf("unsupported format: %s", formatFlag)
	}
}

func loadUserImportRecords(filePath, format string) ([]userImportRecord, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	switch format {
	case "json":
		var records []userImportRecord
		decoder := json.NewDecoder(file)
		if err := decoder.Decode(&records); err != nil {
			return nil, err
		}
		for index := range records {
			records[index].Row = index + 1
			records[index].Username = strings.TrimSpace(records[index].Username)
			records[index].Email = strings.TrimSpace(records[index].Email)
			records[index].Role = strings.TrimSpace(records[index].Role)
			records[index].Type = strings.TrimSpace(records[index].Type)
			records[index].HomeDir = strings.TrimSpace(records[index].HomeDir)
		}
		return records, nil
	case "csv":
		reader := csv.NewReader(file)
		rows, err := reader.ReadAll()
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, errors.New("csv file is empty")
		}
		header := make(map[string]int, len(rows[0]))
		for idx, col := range rows[0] {
			header[strings.ToLower(strings.TrimSpace(col))] = idx
		}

		records := make([]userImportRecord, 0, len(rows)-1)
		for rowIndex, row := range rows[1:] {
			record := userImportRecord{
				Row:          rowIndex + 2,
				Username:     csvValue(row, header, "username"),
				Password:     csvValue(row, header, "password"),
				PasswordHash: csvValue(row, header, "password_hash"),
				Email:        csvValue(row, header, "email"),
				Role:         csvValue(row, header, "role"),
				Type:         csvValue(row, header, "type"),
				HomeDir:      csvValue(row, header, "home_dir"),
			}
			rawEnabled := csvValue(row, header, "enabled")
			enabled, err := parseOptionalBool(rawEnabled)
			if err != nil {
				return nil, fmt.Errorf("row %d: %w", record.Row, err)
			}
			record.Enabled = enabled
			records = append(records, record)
		}
		return records, nil
	default:
		return nil, fmt.Errorf("unsupported import format: %s", format)
	}
}

func createImportedUser(ctx *cliContext, record userImportRecord) (*auth.User, error) {
	username := strings.TrimSpace(record.Username)
	if username == "" {
		return nil, errors.New("username is required")
	}

	userType, err := resolveImportedUserType(record.Role, record.Type)
	if err != nil {
		return nil, err
	}
	homeDir := strings.TrimSpace(record.HomeDir)
	if homeDir == "" {
		homeDir = "/"
	}

	var user *auth.User
	switch {
	case strings.TrimSpace(record.PasswordHash) != "":
		user = &auth.User{
			Username:     username,
			PasswordHash: strings.TrimSpace(record.PasswordHash),
			Email:        strings.TrimSpace(record.Email),
			Type:         userType,
			HomeDir:      homeDir,
			Enabled:      true,
			Permissions:  auth.DefaultUserPermissions(),
		}
		if err := ctx.repo.Create(user); err != nil {
			return nil, err
		}
	case record.Password != "":
		user, err = ctx.engine.CreateUser(username, record.Password, homeDir, userType == auth.UserTypeAdmin)
		if err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("password or password_hash is required")
	}

	needsUpdate := false
	if user.Email != strings.TrimSpace(record.Email) {
		user.Email = strings.TrimSpace(record.Email)
		needsUpdate = true
	}
	if user.Type != userType {
		user.Type = userType
		needsUpdate = true
	}
	if record.Enabled != nil && user.Enabled != *record.Enabled {
		user.Enabled = *record.Enabled
		needsUpdate = true
	}
	if strings.TrimSpace(user.HomeDir) != homeDir {
		user.HomeDir = homeDir
		needsUpdate = true
	}
	if needsUpdate {
		if err := ctx.repo.Update(user); err != nil {
			return nil, err
		}
	}
	return user, nil
}

func resolveImportedUserType(role, rawType string) (auth.UserType, error) {
	value := strings.ToLower(strings.TrimSpace(role))
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(rawType))
	}
	switch value {
	case "", "user", "virtual":
		return auth.UserTypeVirtual, nil
	case "admin":
		return auth.UserTypeAdmin, nil
	default:
		return "", fmt.Errorf("unsupported role/type: %s", value)
	}
}

func exportRoleForUser(user *auth.User) string {
	if user != nil && user.Type == auth.UserTypeAdmin {
		return "admin"
	}
	return "user"
}

func writeUserExportCSV(w io.Writer, records []userExportRecord, includePasswordHashes bool) error {
	writer := csv.NewWriter(w)
	header := []string{"username", "email", "role", "type", "home_dir", "enabled"}
	if includePasswordHashes {
		header = append(header, "password_hash")
	}
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, record := range records {
		row := []string{
			record.Username,
			record.Email,
			record.Role,
			record.Type,
			record.HomeDir,
			fmt.Sprintf("%t", record.Enabled),
		}
		if includePasswordHashes {
			row = append(row, record.PasswordHash)
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func csvValue(row []string, header map[string]int, column string) string {
	idx, ok := header[column]
	if !ok || idx < 0 || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[idx])
}

func parseOptionalBool(raw string) (*bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return nil, nil
	case "1", "true", "yes", "y":
		value := true
		return &value, nil
	case "0", "false", "no", "n":
		value := false
		return &value, nil
	default:
		return nil, fmt.Errorf("invalid enabled value %q", raw)
	}
}
