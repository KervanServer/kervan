package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

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
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
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
		return errors.New("usage: kervan user <list|create|delete> [flags]")
	}
	switch args[0] {
	case "list":
		return runUserListCommand(stdout, args[1:])
	case "create":
		return runUserCreateCommand(stdout, args[1:])
	case "delete":
		return runUserDeleteCommand(stdout, args[1:])
	default:
		return fmt.Errorf("unknown user command: %s", args[0])
	}
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

type cliContext struct {
	cfg    *config.Config
	store  *store.Store
	repo   *auth.UserRepository
	engine *auth.Engine
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
	return &cliContext{
		cfg:    cfg,
		store:  st,
		repo:   repo,
		engine: engine,
	}, nil
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
