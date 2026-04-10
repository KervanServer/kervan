package main

import (
	"context"
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
		return err
	}

	ctx, err := openCLIContext(*configPath)
	if err != nil {
		return err
	}
	defer ctx.close()

	auditLog := filepath.Join(ctx.cfg.Server.DataDir, "audit.jsonl")
	if len(ctx.cfg.Audit.Outputs) > 0 && strings.TrimSpace(ctx.cfg.Audit.Outputs[0].Path) != "" {
		auditLog = ctx.cfg.Audit.Outputs[0].Path
	}

	server := mcp.NewServer(ctx.cfg, ctx.repo, nil, auditLog, stdin, stdout)
	return server.Serve(context.Background())
}
