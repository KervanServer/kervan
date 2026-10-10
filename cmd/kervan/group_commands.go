package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/kervanserver/kervan/internal/auth"
)

func runGroupCommand(stdout io.Writer, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: kervan group <list|create|delete> [flags]")
	}
	switch args[0] {
	case "list":
		return runGroupListCommand(stdout, args[1:])
	case "create":
		return runGroupCreateCommand(stdout, args[1:])
	case "delete":
		return runGroupDeleteCommand(stdout, args[1:])
	default:
		return fmt.Errorf("unknown group command: %s", args[0])
	}
}

// parsePermissionList turns "upload,download,list_dir" into permissions.
func parsePermissionList(raw string) (auth.UserPermissions, error) {
	var p auth.UserPermissions
	for _, item := range strings.Split(raw, ",") {
		switch strings.ToLower(strings.TrimSpace(item)) {
		case "":
		case "upload":
			p.Upload = true
		case "download":
			p.Download = true
		case "delete":
			p.Delete = true
		case "rename":
			p.Rename = true
		case "create_dir", "mkdir":
			p.CreateDir = true
		case "list_dir", "list":
			p.ListDir = true
		case "chmod":
			p.Chmod = true
		default:
			return p, fmt.Errorf("unknown permission %q (upload, download, delete, rename, create_dir, list_dir, chmod)", item)
		}
	}
	return p, nil
}

func permissionNames(p auth.UserPermissions) string {
	var out []string
	for _, item := range []struct {
		on   bool
		name string
	}{
		{p.Upload, "upload"}, {p.Download, "download"}, {p.Delete, "delete"}, {p.Rename, "rename"},
		{p.CreateDir, "create_dir"}, {p.ListDir, "list_dir"}, {p.Chmod, "chmod"},
	} {
		if item.on {
			out = append(out, item.name)
		}
	}
	if len(out) == 0 {
		return "-"
	}
	return strings.Join(out, ",")
}

func formatMaxStorage(v int64) string {
	switch {
	case v < 0:
		return "unlimited"
	case v == 0:
		return "default"
	default:
		return fmt.Sprintf("%d", v)
	}
}

func runGroupListCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("group list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse group list flags: %w", err)
	}
	ctx, err := openCLIContext(*configPath)
	if err != nil {
		return fmt.Errorf("open CLI context: %w", err)
	}
	defer ctx.close()

	groups, err := ctx.groups.List()
	if err != nil {
		return fmt.Errorf("list groups: %w", err)
	}
	if *jsonOut {
		return json.NewEncoder(stdout).Encode(groups)
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tMEMBERS\tMAX_STORAGE\tMAX_BANDWIDTH\tPERMISSIONS\tDESCRIPTION")
	for _, g := range groups {
		members, _ := ctx.groups.Members(g.Name)
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\n", g.Name, len(members), formatMaxStorage(g.MaxStorage), formatMaxStorage(g.MaxBandwidth), permissionNames(g.Permissions), g.Description)
	}
	return tw.Flush()
}

func runGroupCreateCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("group create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	name := fs.String("name", "", "Group name")
	description := fs.String("description", "", "Description")
	perms := fs.String("permissions", "upload,download,delete,rename,create_dir,list_dir", "Comma-separated permissions")
	maxStorage := fs.Int64("max-storage", 0, "Quota in bytes (0 = quota.default_max_storage, -1 = unlimited)")
	maxBandwidth := fs.Int64("max-bandwidth", 0, "Per-user rate limit in bytes/s (0 = bandwidth.default_user_rate, -1 = unlimited)")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse group create flags: %w", err)
	}
	permissions, err := parsePermissionList(*perms)
	if err != nil {
		return err
	}
	ctx, err := openCLIContext(*configPath)
	if err != nil {
		return fmt.Errorf("open CLI context: %w", err)
	}
	defer ctx.close()

	g := &auth.Group{Name: *name, Description: strings.TrimSpace(*description), Permissions: permissions, MaxStorage: *maxStorage, MaxBandwidth: *maxBandwidth}
	if err := ctx.groups.Create(g); err != nil {
		return fmt.Errorf("create group: %w", err)
	}
	if *jsonOut {
		return json.NewEncoder(stdout).Encode(g)
	}
	_, _ = fmt.Fprintf(stdout, "Group created: %s (%s)\n", g.Name, g.ID)
	return nil
}

func runGroupDeleteCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("group delete", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	name := fs.String("name", "", "Group name")
	force := fs.Bool("force", false, "Delete even if users reference the group (their memberships are removed)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse group delete flags: %w", err)
	}
	ctx, err := openCLIContext(*configPath)
	if err != nil {
		return fmt.Errorf("open CLI context: %w", err)
	}
	defer ctx.close()

	g, err := ctx.groups.GetByName(*name)
	if err != nil {
		return err
	}
	if g == nil {
		return fmt.Errorf("group %q does not exist", *name)
	}
	if err := ctx.groups.Delete(g.ID, *force); err != nil {
		if errors.Is(err, auth.ErrGroupInUse) {
			return fmt.Errorf("%w (use --force to remove the memberships)", err)
		}
		return err
	}
	_, _ = fmt.Fprintf(stdout, "Group deleted: %s\n", g.Name)
	return nil
}
