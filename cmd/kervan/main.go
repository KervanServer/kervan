package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/build"
	"github.com/kervanserver/kervan/internal/config"
	icrypto "github.com/kervanserver/kervan/internal/crypto"
	"github.com/kervanserver/kervan/internal/server"
	"github.com/kervanserver/kervan/internal/store"
	ilog "github.com/kervanserver/kervan/internal/util/log"
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
		case "check":
			cmdCheck(os.Args[2:])
			return
		case "migrate":
			cmdMigrate(os.Args[2:])
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
	logger := ilog.New(cfg.Server.LogLevel, cfg.Server.LogFormat, openLogFile(cfg.Server.LogFile))

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
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	force := fs.Bool("force", false, "Overwrite existing config")
	_ = fs.Parse(args)

	if !*force {
		if _, err := os.Stat(*configPath); err == nil {
			exitf("config already exists: %s (use --force to overwrite)", *configPath)
		}
	}

	if *force {
		_ = os.Remove(*configPath)
	}
	if err := config.WriteDefault(*configPath); err != nil {
		exitf("init config: %v", err)
	}
	fmt.Printf("Config created: %s\n", *configPath)
}

func cmdKeygen(args []string) {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	keyType := fs.String("type", "ed25519", "ed25519|rsa")
	outputDir := fs.String("output", "./data/host_keys", "Output directory")
	_ = fs.Parse(args)

	if err := os.MkdirAll(*outputDir, 0o700); err != nil {
		exitf("create output dir: %v", err)
	}

	switch *keyType {
	case "ed25519":
		path := filepath.Join(*outputDir, "ssh_host_ed25519_key")
		if err := icrypto.GenerateED25519HostKey(path); err != nil {
			exitf("generate ed25519 key: %v", err)
		}
		fmt.Printf("Generated: %s\n", path)
	case "rsa":
		path := filepath.Join(*outputDir, "ssh_host_rsa_key")
		if err := icrypto.GenerateRSAHostKey(path, 4096); err != nil {
			exitf("generate rsa key: %v", err)
		}
		fmt.Printf("Generated: %s\n", path)
	default:
		exitf("unknown key type: %s", *keyType)
	}
}

func cmdAdmin(args []string) {
	if len(args) == 0 {
		exitf("usage: kervan admin <create|reset-password> [flags]")
	}
	switch args[0] {
	case "create":
		cmdAdminCreate(args[1:])
	case "reset-password":
		cmdAdminReset(args[1:])
	default:
		exitf("unknown admin command: %s", args[0])
	}
}

func cmdAdminCreate(args []string) {
	fs := flag.NewFlagSet("admin create", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	username := fs.String("username", "admin", "Admin username")
	password := fs.String("password", "", "Admin password")
	_ = fs.Parse(args)
	if *password == "" {
		exitf("--password is required")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		exitf("load config: %v", err)
	}
	st, err := store.Open(cfg.Server.DataDir)
	if err != nil {
		exitf("open store: %v", err)
	}
	defer st.Close()
	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, cfg.Auth.PasswordHash, cfg.Security.BruteForce.MaxAttempts, cfg.Security.BruteForce.LockoutDuration)
	u, err := engine.CreateUser(*username, *password, "/", true)
	if err != nil {
		exitf("create admin: %v", err)
	}
	fmt.Printf("Admin created: %s (%s)\n", u.Username, u.ID)
}

func cmdAdminReset(args []string) {
	fs := flag.NewFlagSet("admin reset-password", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	username := fs.String("username", "admin", "Admin username")
	password := fs.String("password", "", "New password")
	_ = fs.Parse(args)
	if *password == "" {
		exitf("--password is required")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		exitf("load config: %v", err)
	}
	st, err := store.Open(cfg.Server.DataDir)
	if err != nil {
		exitf("open store: %v", err)
	}
	defer st.Close()
	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, cfg.Auth.PasswordHash, cfg.Security.BruteForce.MaxAttempts, cfg.Security.BruteForce.LockoutDuration)
	if err := engine.ResetPassword(*username, *password); err != nil {
		exitf("reset password: %v", err)
	}
	fmt.Printf("Password reset: %s\n", *username)
}

func openLogFile(path string) *os.File {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil
	}
	return f
}

func exitf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	time.Sleep(50 * time.Millisecond)
	os.Exit(1)
}
