package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/build"
	"github.com/kervanserver/kervan/internal/config"
	icrypto "github.com/kervanserver/kervan/internal/crypto"
	"github.com/kervanserver/kervan/internal/server"
	ilog "github.com/kervanserver/kervan/internal/util/log"
	"golang.org/x/crypto/ssh"
)

const defaultConfigPath = "kervan.yaml"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Println(build.Info())
			return
		case "init":
			cmdInit(os.Args[2:])
			return
		case "keygen":
			cmdKeygen(os.Args[2:])
			return
		case "admin":
			cmdAdmin(os.Args[2:])
			return
		case "user":
			cmdUser(os.Args[2:])
			return
		case "apikey":
			cmdAPIKey(os.Args[2:])
			return
		case "backup":
			cmdBackup(os.Args[2:])
			return
		case "check":
			cmdCheck(os.Args[2:])
			return
		case "migrate":
			cmdMigrate(os.Args[2:])
			return
		case "mcp":
			cmdMCP(os.Args[2:])
			return
		case "status":
			cmdStatus(os.Args[2:])
			return
		}
	}
	cmdRun(os.Args[1:])
}

func cmdRun(args []string) {
	fs := flag.NewFlagSet("kervan", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	_ = fs.Parse(args)

	if _, err := os.Stat(*configPath); errors.Is(err, os.ErrNotExist) {
		if err := config.WriteDefault(*configPath); err != nil {
			exitf("write default config: %v", err)
		}
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		exitf("load config: %v", err)
	}
	logOutput, err := ilog.OpenFile(cfg.Server.LogFile, cfg.Server.LogMaxSizeMB, cfg.Server.LogMaxBackups)
	if err != nil {
		exitf("open log output: %v", err)
	}
	if logOutput != nil {
		defer func() {
			_ = logOutput.Close()
		}()
	}
	logger := ilog.New(cfg.Server.LogLevel, cfg.Server.LogFormat, logOutput)

	app, err := server.New(cfg, *configPath, logger)
	if err != nil {
		exitf("create app: %v", err)
	}
	defer func() {
		if closeErr := app.Close(); closeErr != nil && logger != nil {
			logger.Error("close error", "error", closeErr)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Start(ctx); err != nil {
		exitf("start: %v", err)
	}

	logger.Info("server is running", "version", build.Version)
	<-ctx.Done()
	logger.Info("shutdown signal received")
}

func cmdInit(args []string) {
	if err := runInitCommand(os.Stdout, args); err != nil {
		exitf("init config: %v", err)
	}
}

func cmdKeygen(args []string) {
	if err := runKeygenCommand(os.Stdout, args); err != nil {
		exitf("keygen: %v", err)
	}
}

func cmdAdmin(args []string) {
	if err := runAdminCommand(os.Stdout, args); err != nil {
		exitf("admin: %v", err)
	}
}

func cmdAPIKey(args []string) {
	if err := runAPIKeyCommand(os.Stdout, args); err != nil {
		exitf("apikey: %v", err)
	}
}

func exitf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	time.Sleep(50 * time.Millisecond)
	os.Exit(1)
}

func runInitCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	force := fs.Bool("force", false, "Overwrite existing config")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if !*force {
		if _, err := os.Stat(*configPath); err == nil {
			return fmt.Errorf("config already exists: %s (use --force to overwrite)", *configPath)
		}
	}

	if *force {
		_ = os.Remove(*configPath)
	}
	if err := config.WriteDefault(*configPath); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if err := ensureInitDataDirs(cfg); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stdout, "Config created: %s\n", *configPath)
	return nil
}

func ensureInitDataDirs(cfg *config.Config) error {
	dirs := []string{
		cfg.Server.DataDir,
		cfg.SFTP.HostKeyDir,
	}
	for _, backend := range cfg.Storage.Backends {
		if backend.Type != "local" {
			continue
		}
		root := ""
		if backend.Options != nil {
			root = backend.Options["root"]
		}
		if root != "" {
			dirs = append(dirs, root)
		}
	}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func runKeygenCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	keyType := fs.String("type", "ed25519", "ed25519|rsa|both")
	outputDir := fs.String("output", "./data/host_keys", "Output directory")
	force := fs.Bool("force", false, "Overwrite existing keys")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := os.MkdirAll(*outputDir, 0o700); err != nil {
		return err
	}

	var generated []string
	switch *keyType {
	case "ed25519":
		path, err := generateHostKeyFile(*outputDir, "ed25519", *force)
		if err != nil {
			return err
		}
		generated = append(generated, path)
	case "rsa":
		path, err := generateHostKeyFile(*outputDir, "rsa", *force)
		if err != nil {
			return err
		}
		generated = append(generated, path)
	case "both":
		for _, kind := range []string{"ed25519", "rsa"} {
			path, err := generateHostKeyFile(*outputDir, kind, *force)
			if err != nil {
				return err
			}
			generated = append(generated, path)
		}
	default:
		return fmt.Errorf("unknown key type: %s", *keyType)
	}

	for _, path := range generated {
		signer, err := icrypto.LoadSigner(path)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "Generated: %s\n", path)
		_, _ = fmt.Fprintf(stdout, "Fingerprint: %s\n", ssh.FingerprintSHA256(signer.PublicKey()))
	}
	return nil
}

func generateHostKeyFile(outputDir, keyType string, force bool) (string, error) {
	var path string
	switch keyType {
	case "ed25519":
		path = filepath.Join(outputDir, "ssh_host_ed25519_key")
	case "rsa":
		path = filepath.Join(outputDir, "ssh_host_rsa_key")
	default:
		return "", fmt.Errorf("unknown key type: %s", keyType)
	}

	if !force {
		if _, err := os.Stat(path); err == nil {
			return "", fmt.Errorf("host key already exists: %s (use --force to overwrite)", path)
		}
	}

	switch keyType {
	case "ed25519":
		return path, icrypto.GenerateED25519HostKey(path)
	case "rsa":
		return path, icrypto.GenerateRSAHostKey(path, 4096)
	default:
		return "", fmt.Errorf("unknown key type: %s", keyType)
	}
}

func runAdminCommand(stdout io.Writer, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: kervan admin <create|reset-password|list> [flags]")
	}
	switch args[0] {
	case "create":
		return runAdminCreateCommand(stdout, args[1:])
	case "reset-password":
		return runAdminResetCommand(stdout, args[1:])
	case "list":
		return runAdminListCommand(stdout, args[1:])
	default:
		return fmt.Errorf("unknown admin command: %s", args[0])
	}
}

func runAdminCreateCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("admin create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	username := fs.String("username", "admin", "Admin username")
	password := fs.String("password", "", "Admin password")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *password == "" {
		return errors.New("--password is required")
	}

	ctx, err := openCLIContext(*configPath)
	if err != nil {
		return err
	}
	defer ctx.close()

	u, err := ctx.engine.CreateUser(*username, *password, "/", true)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "Admin created: %s (%s)\n", u.Username, u.ID)
	return nil
}

func runAdminResetCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("admin reset-password", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	username := fs.String("username", "admin", "Admin username")
	password := fs.String("password", "", "New password")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *password == "" {
		return errors.New("--password is required")
	}

	ctx, err := openCLIContext(*configPath)
	if err != nil {
		return err
	}
	defer ctx.close()

	if err := ctx.engine.ResetPassword(*username, *password); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "Password reset: %s\n", *username)
	return nil
}

func runAdminListCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("admin list", flag.ContinueOnError)
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

	admins := make([]*auth.User, 0, len(users))
	for _, user := range users {
		if user != nil && user.Type == auth.UserTypeAdmin {
			admins = append(admins, user)
		}
	}
	sort.Slice(admins, func(i, j int) bool {
		return admins[i].Username < admins[j].Username
	})

	if *jsonOut {
		type adminListItem struct {
			ID        string        `json:"id"`
			Username  string        `json:"username"`
			Enabled   bool          `json:"enabled"`
			CreatedAt time.Time     `json:"created_at"`
			UpdatedAt time.Time     `json:"updated_at"`
			Type      auth.UserType `json:"type"`
		}
		items := make([]adminListItem, 0, len(admins))
		for _, user := range admins {
			items = append(items, adminListItem{
				ID:        user.ID,
				Username:  user.Username,
				Enabled:   user.Enabled,
				CreatedAt: user.CreatedAt,
				UpdatedAt: user.UpdatedAt,
				Type:      user.Type,
			})
		}
		return json.NewEncoder(stdout).Encode(items)
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "USERNAME\tENABLED\tCREATED\tUPDATED")
	for _, user := range admins {
		_, _ = fmt.Fprintf(tw, "%s\t%t\t%s\t%s\n",
			user.Username,
			user.Enabled,
			user.CreatedAt.Format(time.RFC3339),
			user.UpdatedAt.Format(time.RFC3339),
		)
	}
	return tw.Flush()
}
