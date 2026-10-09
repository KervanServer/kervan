package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kervanserver/kervan/internal/audit"
)

type auditFilter struct {
	Limit    int
	Username string
	Type     string
}

func (s *Server) resourceDefinitions() []map[string]any {
	return []map[string]any{
		{
			"uri":         "kervan://config/summary",
			"name":        "Config Summary",
			"description": "Current server configuration summary without secrets.",
			"mimeType":    "application/json",
		},
		{
			"uri":         "kervan://users",
			"name":        "Users",
			"description": "Local user list without secrets.",
			"mimeType":    "application/json",
		},
		{
			"uri":         "kervan://audit/recent",
			"name":        "Recent Audit",
			"description": "Recent audit events from the audit JSONL file.",
			"mimeType":    "application/json",
		},
	}
}

func (s *Server) handleResourceRead(raw json.RawMessage) (any, *rpcError) {
	params, err := decodeParams[struct {
		URI string `json:"uri"`
	}](raw)
	if err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid resources/read params"}
	}

	var payload any
	switch params.URI {
	case "kervan://config/summary":
		payload = s.configSummary()
	case "kervan://users":
		users, err := s.listUsers(false)
		if err != nil {
			return nil, &rpcError{Code: -32603, Message: err.Error()}
		}
		payload = map[string]any{"users": users}
	case "kervan://audit/recent":
		events, err := readRecentAuditEvents(s.auditLog, auditFilter{Limit: 50})
		if err != nil {
			return nil, &rpcError{Code: -32603, Message: err.Error()}
		}
		payload = map[string]any{"events": events}
	default:
		return nil, &rpcError{Code: -32602, Message: "unknown resource"}
	}

	text, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: "failed to encode resource"}
	}
	return map[string]any{
		"contents": []map[string]any{
			{
				"uri":      params.URI,
				"mimeType": "application/json",
				"text":     string(text),
			},
		},
	}, nil
}

func (s *Server) listUsers(enabledOnly bool) ([]map[string]any, error) {
	if s.repo == nil {
		return nil, errors.New("user repository is not configured")
	}
	users, err := s.repo.List()
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(users))
	for _, user := range users {
		if user == nil {
			continue
		}
		if enabledOnly && !user.Enabled {
			continue
		}
		out = append(out, map[string]any{
			"id":            user.ID,
			"username":      user.Username,
			"email":         user.Email,
			"type":          user.Type,
			"auth_provider": user.AuthProvider,
			"enabled":       user.Enabled,
			"home_dir":      user.HomeDir,
			"created_at":    user.CreatedAt,
			"updated_at":    user.UpdatedAt,
			"last_login_at": user.LastLoginAt,
			"permissions":   user.Permissions,
		})
	}
	return out, nil
}

func (s *Server) configSummary() map[string]any {
	if s.cfg == nil {
		return map[string]any{}
	}
	return map[string]any{
		"name":            s.cfg.Server.Name,
		"data_dir":        s.cfg.Server.DataDir,
		"listen_address":  s.cfg.Server.ListenAddress,
		"ftp_enabled":     s.cfg.FTP.Enabled,
		"ftp_port":        s.cfg.FTP.Port,
		"sftp_enabled":    s.cfg.SFTP.Enabled,
		"sftp_port":       s.cfg.SFTP.Port,
		"scp_enabled":     s.cfg.SCP.Enabled,
		"webui_enabled":   s.cfg.WebUI.Enabled,
		"webui_port":      s.cfg.WebUI.Port,
		"storage_backend": s.cfg.Storage.DefaultBackend,
		"mcp_enabled":     s.cfg.MCP.Enabled,
		"mcp_transport":   s.cfg.MCP.Transport,
		"audit_log":       s.auditLog,
		"loaded_at":       time.Now().UTC(),
	}
}

func readRecentAuditEvents(auditLog string, filter auditFilter) ([]audit.Event, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if strings.TrimSpace(auditLog) == "" {
		return []audit.Event{}, nil
	}
	file, err := audit.OpenLog(filepath.Clean(auditLog))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []audit.Event{}, nil
		}
		return nil, err
	}
	defer file.Close()

	// Keep only the newest Limit matches while streaming, so memory does
	// not grow with the size of the (rotated) log.
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	ring := make([]audit.Event, 0, filter.Limit)
	next := 0
	for scanner.Scan() {
		var evt audit.Event
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			continue
		}
		if filter.Username != "" && !strings.EqualFold(evt.Username, filter.Username) {
			continue
		}
		if filter.Type != "" && !strings.EqualFold(string(evt.Type), filter.Type) {
			continue
		}
		if len(ring) < filter.Limit {
			ring = append(ring, evt)
		} else {
			ring[next] = evt
			next = (next + 1) % filter.Limit
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	// Newest first.
	events := make([]audit.Event, 0, len(ring))
	for i := 0; i < len(ring); i++ {
		events = append(events, ring[(next+len(ring)-1-i)%len(ring)])
	}
	return events, nil
}
