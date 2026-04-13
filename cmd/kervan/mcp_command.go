package main

import (
	"context"
	"fmt"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kervanserver/kervan/internal/mcp"
)

func cmdMCP(args []string) {
	if err := runMCPCommand(os.Stdin, os.Stdout, args); err != nil {
		exitf("mcp: %v", err)
	}
}

func runMCPCommand(stdin io.Reader, stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse mcp flags: %w", err)
	}

	ctx, err := openCLIContext(*configPath)
	if err != nil {
		return fmt.Errorf("open CLI context: %w", err)
	}
	defer ctx.close()

	auditLog := filepath.Join(ctx.cfg.Server.DataDir, "audit.jsonl")
	if len(ctx.cfg.Audit.Outputs) > 0 && strings.TrimSpace(ctx.cfg.Audit.Outputs[0].Path) != "" {
		auditLog = ctx.cfg.Audit.Outputs[0].Path
	}

	server := mcp.NewServer(ctx.cfg, ctx.repo, nil, auditLog, stdin, stdout)
	if err := server.Serve(context.Background()); err != nil {
		return fmt.Errorf("serve MCP stdio session: %w", err)
	}
	return nil
}
