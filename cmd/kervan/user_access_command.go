package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
)

// runUserAccessCommand shows or changes a user's sign-in policy: expiry,
// allowed addresses, allowed protocols and the concurrent-session cap. It
// works offline, so it also recovers an admin who locked themselves out.
func runUserAccessCommand(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("user access", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	username := fs.String("username", "", "Username")
	expires := fs.String("expires", "", `Expiry as RFC 3339 (2026-12-31T23:59:59Z) or a date (2026-12-31, end of day UTC); "never" clears`)
	ips := fs.String("allowed-ips", "", `Comma-separated IPs/CIDRs; "any" clears`)
	protocols := fs.String("protocols", "", `Comma-separated subset of `+strings.Join(auth.KnownProtocols, ",")+`; "all" clears`)
	maxSessions := fs.String("max-sessions", "", "Concurrent FTP/SFTP/SCP sessions (0 = unlimited)")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse user access flags: %w", err)
	}
	if strings.TrimSpace(*username) == "" {
		return errors.New("--username is required")
	}

	ctx, err := openCLIContext(*configPath)
	if err != nil {
		return fmt.Errorf("open CLI context: %w", err)
	}
	defer ctx.close()

	user, err := ctx.repo.GetByUsername(strings.TrimSpace(*username))
	if err != nil {
		return fmt.Errorf("look up user: %w", err)
	}
	if user == nil {
		return fmt.Errorf("user %q not found", *username)
	}

	changed := false
	if v := strings.TrimSpace(*expires); v != "" {
		t, err := parseExpiry(v)
		if err != nil {
			return err
		}
		user.ExpiresAt, changed = t, true
	}
	nextIPs, nextProtocols, nextMax := user.AllowedIPs, user.AllowedProtocols, user.MaxSessions
	if v := strings.TrimSpace(*ips); v != "" {
		nextIPs, changed = splitList(v, "any"), true
	}
	if v := strings.TrimSpace(*protocols); v != "" {
		nextProtocols, changed = splitList(v, "all"), true
	}
	if v := strings.TrimSpace(*maxSessions); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("--max-sessions: %w", err)
		}
		nextMax, changed = n, true
	}
	if changed {
		nextIPs, nextProtocols, err = auth.NormalizeAccessPolicy(nextIPs, nextProtocols, nextMax)
		if err != nil {
			return err
		}
		user.AllowedIPs, user.AllowedProtocols, user.MaxSessions = nextIPs, nextProtocols, nextMax
		if err := ctx.repo.Update(user); err != nil {
			return fmt.Errorf("update user: %w", err)
		}
	}

	if *jsonOut {
		return json.NewEncoder(stdout).Encode(map[string]any{
			"username":          user.Username,
			"expires_at":        user.ExpiresAt,
			"expired":           user.Expired(time.Now()),
			"allowed_ips":       user.AllowedIPs,
			"allowed_protocols": user.AllowedProtocols,
			"max_sessions":      user.MaxSessions,
		})
	}
	expiry := "never"
	if user.ExpiresAt != nil {
		expiry = user.ExpiresAt.Format(time.RFC3339)
		if user.Expired(time.Now()) {
			expiry += " (expired)"
		}
	}
	orAll := func(v []string, all string) string {
		if len(v) == 0 {
			return all
		}
		return strings.Join(v, ", ")
	}
	sessions := "unlimited"
	if user.MaxSessions > 0 {
		sessions = strconv.Itoa(user.MaxSessions)
	}
	_, _ = fmt.Fprintf(stdout, "User:         %s\nExpires:      %s\nAllowed IPs:  %s\nProtocols:    %s\nMax sessions: %s\n",
		user.Username, expiry, orAll(user.AllowedIPs, "any"), orAll(user.AllowedProtocols, "all"), sessions)
	return nil
}

func parseExpiry(v string) (*time.Time, error) {
	if strings.EqualFold(v, "never") {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		t = t.UTC()
		return &t, nil
	}
	if d, err := time.Parse(time.DateOnly, v); err == nil {
		t := d.Add(24*time.Hour - time.Second).UTC()
		return &t, nil
	}
	return nil, fmt.Errorf("--expires %q: use RFC 3339, YYYY-MM-DD or never", v)
}

func splitList(v, clearWord string) []string {
	if strings.EqualFold(v, clearWord) {
		return nil
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
