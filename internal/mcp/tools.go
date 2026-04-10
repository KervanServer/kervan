package mcp

import (
	"context"
	"encoding/json"
	"time"
)

func (s *Server) toolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":        "list_users",
			"description": "List local Kervan users without secrets.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"enabled_only": map[string]any{"type": "boolean"},
				},
			},
		},
		{
			"name":        "audit_query",
			"description": "Read recent audit events and optionally filter by username or type.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit":    map[string]any{"type": "integer", "minimum": 1, "maximum": 500},
					"username": map[string]any{"type": "string"},
					"type":     map[string]any{"type": "string"},
				},
			},
		},
		{
			"name":        "transfer_stats",
			"description": "Return current in-process transfer manager stats when available.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

func (s *Server) handleToolCall(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid tool call params"}
	}
	switch params.Name {
	case "list_users":
		args, err := decodeParams[struct {
			EnabledOnly bool `json:"enabled_only"`
		}](params.Arguments)
		if err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid list_users arguments"}
		}
		users, err := s.listUsers(args.EnabledOnly)
		if err != nil {
			return nil, &rpcError{Code: -32603, Message: err.Error()}
		}
		return makeTextResult(map[string]any{"users": users})
	case "audit_query":
		args, err := decodeParams[struct {
			Limit    int    `json:"limit"`
			Username string `json:"username"`
			Type     string `json:"type"`
		}](params.Arguments)
		if err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid audit_query arguments"}
		}
		if args.Limit <= 0 {
			args.Limit = 50
		}
		events, err := readRecentAuditEvents(s.auditLog, auditFilter{
			Limit:    args.Limit,
			Username: args.Username,
			Type:     args.Type,
		})
		if err != nil {
			return nil, &rpcError{Code: -32603, Message: err.Error()}
		}
		return makeTextResult(map[string]any{"events": events})
	case "transfer_stats":
		stats := map[string]any{
			"source": "standalone",
			"note":   "live transfer stats are only available when the MCP process shares runtime with the server",
		}
		if s.transfers != nil {
			stats = map[string]any{
				"source": "transfer_manager",
				"stats":  s.transfers.Stats(),
				"at":     time.Now().UTC(),
			}
		}
		return makeTextResult(stats)
	default:
		return nil, &rpcError{Code: -32601, Message: "unknown tool"}
	}
}
