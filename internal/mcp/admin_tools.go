package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kervanserver/kervan/internal/audit"
	"github.com/kervanserver/kervan/internal/auth"
)

// SetGroups enables the group-aware tools (list_groups, and the effective
// policy in get_user).
func (s *Server) SetGroups(groups *auth.GroupRepository) { s.groups = groups }

func (s *Server) extraToolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":        "get_user",
			"description": "Show one user without secrets: group, quotas, sign-in restrictions (expiry, allowed IPs and protocols, session cap), SSH key fingerprints and the effective policy after group inheritance.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"username"},
				"properties": map[string]any{
					"username": map[string]any{"type": "string"},
				},
			},
		},
		{
			"name":        "list_groups",
			"description": "List groups with their permission, quota and bandwidth templates and member counts.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			"name":        "verify_audit",
			"description": "Verify the tamper-evident audit chain (all rotated files plus the live log) and report problems.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
}

// handleExtraTool runs the tools from extraToolDefinitions; ok is false for
// an unknown name.
func (s *Server) handleExtraTool(name string, raw json.RawMessage) (result any, rpcErr *rpcError, ok bool) {
	switch name {
	case "get_user":
		args, err := decodeParams[struct {
			Username string `json:"username"`
		}](raw)
		if err != nil || strings.TrimSpace(args.Username) == "" {
			return nil, &rpcError{Code: -32602, Message: "get_user needs a username"}, true
		}
		out, err := s.describeUser(strings.TrimSpace(args.Username))
		if err != nil {
			return nil, &rpcError{Code: -32603, Message: err.Error()}, true
		}
		res, rerr := makeTextResult(out)
		return res, rerr, true
	case "list_groups":
		out, err := s.listGroups()
		if err != nil {
			return nil, &rpcError{Code: -32603, Message: err.Error()}, true
		}
		res, rerr := makeTextResult(map[string]any{"groups": out})
		return res, rerr, true
	case "verify_audit":
		report, err := s.verifyAudit()
		if err != nil {
			return nil, &rpcError{Code: -32603, Message: err.Error()}, true
		}
		res, rerr := makeTextResult(map[string]any{"ok": report.OK(), "report": report})
		return res, rerr, true
	default:
		return nil, nil, false
	}
}

func (s *Server) describeUser(username string) (map[string]any, error) {
	if s.repo == nil {
		return nil, errors.New("user repository is not configured")
	}
	u, err := s.repo.GetByUsername(username)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, fmt.Errorf("user %q not found", username)
	}
	keys := make([]map[string]string, 0, len(u.AuthorizedKeys))
	for _, k := range auth.DescribeAuthorizedKeys(u.AuthorizedKeys) {
		keys = append(keys, map[string]string{"type": k.Type, "fingerprint": k.Fingerprint, "comment": k.Comment})
	}
	out := map[string]any{
		"id":                 u.ID,
		"username":           u.Username,
		"email":              u.Email,
		"type":               u.Type,
		"auth_provider":      u.AuthProvider,
		"enabled":            u.Enabled,
		"home_dir":           u.HomeDir,
		"primary_group":      u.PrimaryGroup,
		"secondary_groups":   u.SecondaryGrps,
		"last_login_at":      u.LastLoginAt,
		"locked_until":       u.LockedUntil,
		"totp_enabled":       u.TOTPEnabled,
		"authorized_keys":    keys,
		"max_storage":        u.MaxStorage,
		"max_bandwidth":      u.MaxBandwidth,
		"max_files":          u.MaxFiles,
		"custom_permissions": u.CustomPermissions,
		"permissions":        u.Permissions,
		"access": map[string]any{
			"expires_at":        u.ExpiresAt,
			"expired":           u.Expired(time.Now()),
			"allowed_ips":       u.AllowedIPs,
			"allowed_protocols": u.AllowedProtocols,
			"max_sessions":      u.MaxSessions,
		},
	}
	if s.groups != nil && s.cfg != nil {
		defaults := auth.PolicyDefaults{
			MaxStorage:   s.cfg.Quota.DefaultMaxStorage,
			MaxBandwidth: s.cfg.Bandwidth.DefaultUserRate,
			MaxFiles:     s.cfg.Quota.DefaultMaxFiles,
		}
		if policy, err := s.groups.PolicyFor(u, defaults); err == nil {
			eff := map[string]any{
				"permissions":   policy.Permissions,
				"max_storage":   policy.MaxStorage,
				"max_bandwidth": policy.MaxBandwidth,
				"max_files":     policy.MaxFiles,
			}
			// Mirrors the server: quotas do not apply when disabled or to admins.
			if !s.cfg.Quota.Enabled || u.Type == auth.UserTypeAdmin {
				eff["max_storage"], eff["max_files"] = int64(0), int64(0)
			}
			if policy.Group != nil {
				eff["group"] = policy.Group.Name
			}
			out["effective"] = eff
		}
	}
	return out, nil
}

func (s *Server) listGroups() ([]map[string]any, error) {
	if s.groups == nil {
		return nil, errors.New("groups are not configured")
	}
	groups, err := s.groups.List()
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		members, _ := s.groups.Members(g.Name)
		out = append(out, map[string]any{
			"id":            g.ID,
			"name":          g.Name,
			"description":   g.Description,
			"permissions":   g.Permissions,
			"max_storage":   g.MaxStorage,
			"max_bandwidth": g.MaxBandwidth,
			"max_files":     g.MaxFiles,
			"member_count":  len(members),
		})
	}
	return out, nil
}

func (s *Server) verifyAudit() (audit.VerifyReport, error) {
	if s.cfg == nil || strings.TrimSpace(s.auditLog) == "" {
		return audit.VerifyReport{}, errors.New("no audit log is configured")
	}
	keyPath := s.cfg.AuditKeyPath()
	// Verification must never create a key.
	if _, err := os.Stat(keyPath); err != nil {
		return audit.VerifyReport{}, fmt.Errorf("audit integrity key %s: %w", keyPath, err)
	}
	key, err := audit.LoadOrCreateChainKey(keyPath)
	if err != nil {
		return audit.VerifyReport{}, err
	}
	f, err := audit.OpenLog(s.auditLog)
	if err != nil {
		return audit.VerifyReport{}, err
	}
	defer f.Close()
	return audit.VerifyChain(f, key)
}

// parseToolTime accepts RFC 3339 or YYYY-MM-DD; a bare date used as an
// upper bound covers that whole day.
func parseToolTime(raw string, endOfDay bool) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	d, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q: use RFC 3339 or YYYY-MM-DD", raw)
	}
	if endOfDay {
		d = d.AddDate(0, 0, 1)
	}
	return d, nil
}
