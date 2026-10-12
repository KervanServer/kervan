package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

func (s *Server) toolDefinitions() []map[string]any {
	return append([]map[string]any{
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
			"description": "Read the newest audit events, optionally filtered by username, event type, status and time range.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit":    map[string]any{"type": "integer", "minimum": 1, "maximum": 500},
					"username": map[string]any{"type": "string"},
					"type":     map[string]any{"type": "string", "description": "e.g. auth.failure, file.write, admin.user.update"},
					"status":   map[string]any{"type": "string", "description": "ok, failed, rejected, incomplete, aborted"},
					"from":     map[string]any{"type": "string", "description": "RFC 3339 time or YYYY-MM-DD (inclusive)"},
					"to":       map[string]any{"type": "string", "description": "RFC 3339 time (exclusive) or YYYY-MM-DD (whole day included)"},
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
	}, s.extraToolDefinitions()...)
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
			Status   string `json:"status"`
			From     string `json:"from"`
			To       string `json:"to"`
		}](params.Arguments)
		if err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid audit_query arguments"}
		}
		if args.Limit <= 0 {
			args.Limit = 50
		}
		from, fromErr := parseToolTime(args.From, false)
		to, toErr := parseToolTime(args.To, true)
		if fromErr != nil || toErr != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid audit_query time range: " + errors.Join(fromErr, toErr).Error()}
		}
		events, err := readRecentAuditEvents(s.auditLog, auditFilter{
			Limit:    min(args.Limit, 500),
			Username: args.Username,
			Type:     args.Type,
			Status:   args.Status,
			From:     from,
			To:       to,
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
		if result, rpcErr, ok := s.handleExtraTool(params.Name, params.Arguments); ok {
			return result, rpcErr
		}
		return nil, &rpcError{Code: -32601, Message: "unknown tool"}
	}
}
