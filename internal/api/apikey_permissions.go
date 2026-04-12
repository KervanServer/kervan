package api

import (
	"errors"
	"net/http"
	"strings"
)

type APIKeyScopeInfo struct {
	Name        string `json:"name"`
	Resource    string `json:"resource"`
	Access      string `json:"access"`
	Description string `json:"description"`
}

type APIKeyPermissionPreset struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Scopes      []string `json:"scopes"`
}

var apiKeyScopeOrder = []string{
	"server:read",
	"server:write",
	"users:read",
	"users:write",
	"apikeys:read",
	"apikeys:write",
	"sessions:read",
	"sessions:write",
	"files:read",
	"files:write",
	"share:read",
	"share:write",
	"audit:read",
	"transfers:read",
}

var apiKeyReadScopes = []string{
	"server:read",
	"users:read",
	"apikeys:read",
	"sessions:read",
	"files:read",
	"share:read",
	"audit:read",
	"transfers:read",
}

var apiKeyScopeDescriptions = map[string]APIKeyScopeInfo{
	"server:read":    {Name: "server:read", Resource: "server", Access: "read", Description: "Read health, status, and runtime metadata."},
	"server:write":   {Name: "server:write", Resource: "server", Access: "write", Description: "Change runtime config or trigger reload actions."},
	"users:read":     {Name: "users:read", Resource: "users", Access: "read", Description: "List and export user accounts."},
	"users:write":    {Name: "users:write", Resource: "users", Access: "write", Description: "Create, update, import, or delete users."},
	"apikeys:read":   {Name: "apikeys:read", Resource: "apikeys", Access: "read", Description: "List existing API keys."},
	"apikeys:write":  {Name: "apikeys:write", Resource: "apikeys", Access: "write", Description: "Create or revoke API keys."},
	"sessions:read":  {Name: "sessions:read", Resource: "sessions", Access: "read", Description: "View live protocol sessions."},
	"sessions:write": {Name: "sessions:write", Resource: "sessions", Access: "write", Description: "Terminate active protocol sessions."},
	"files:read":     {Name: "files:read", Resource: "files", Access: "read", Description: "List, stat, and download files."},
	"files:write":    {Name: "files:write", Resource: "files", Access: "write", Description: "Upload, rename, mkdir, and delete files."},
	"share:read":     {Name: "share:read", Resource: "share", Access: "read", Description: "List issued share links."},
	"share:write":    {Name: "share:write", Resource: "share", Access: "write", Description: "Create or revoke share links."},
	"audit:read":     {Name: "audit:read", Resource: "audit", Access: "read", Description: "Read and export audit events."},
	"transfers:read": {Name: "transfers:read", Resource: "transfers", Access: "read", Description: "Inspect transfer activity and stats."},
}

var apiKeyPermissionPresets = []APIKeyPermissionPreset{
	{
		ID:          "read-only",
		Label:       "Read only",
		Description: "Broad observability and read access without mutating actions.",
		Scopes:      append([]string(nil), apiKeyReadScopes...),
	},
	{
		ID:          "automation",
		Label:       "Automation",
		Description: "File automation plus share-link management for integrations.",
		Scopes:      []string{"files:read", "files:write", "share:read", "share:write", "transfers:read"},
	},
	{
		ID:          "operations",
		Label:       "Operations",
		Description: "Operational insight with session control and audit visibility.",
		Scopes:      []string{"server:read", "sessions:read", "sessions:write", "audit:read", "transfers:read"},
	},
	{
		ID:          "read-write",
		Label:       "Full access",
		Description: "Equivalent to the legacy read-write API key.",
		Scopes:      append([]string(nil), apiKeyScopeOrder...),
	},
}

var apiKeyScopeExpansions = map[string][]string{
	"*":           apiKeyScopeOrder,
	"read-write":  apiKeyScopeOrder,
	"readwrite":   apiKeyScopeOrder,
	"write":       apiKeyScopeOrder,
	"read-only":   apiKeyReadScopes,
	"readonly":    apiKeyReadScopes,
	"read":        apiKeyReadScopes,
	"server:*":    []string{"server:read", "server:write"},
	"users:*":     []string{"users:read", "users:write"},
	"apikeys:*":   []string{"apikeys:read", "apikeys:write"},
	"sessions:*":  []string{"sessions:read", "sessions:write"},
	"files:*":     []string{"files:read", "files:write"},
	"share:*":     []string{"share:read", "share:write"},
	"audit:*":     []string{"audit:read"},
	"transfers:*": []string{"transfers:read"},
}

type apiKeyPermissionSet map[string]struct{}

func normalizeAPIKeyPermissions(raw string) (string, error) {
	set, err := parseAPIKeyPermissionSet(raw)
	if err != nil {
		return "", err
	}
	if apiKeyScopeSetEquals(set, apiKeyScopeOrder) {
		return "read-write", nil
	}
	if apiKeyScopeSetEquals(set, apiKeyReadScopes) {
		return "read-only", nil
	}

	ordered := make([]string, 0, len(set))
	for _, scope := range apiKeyScopeOrder {
		if _, ok := set[scope]; ok {
			ordered = append(ordered, scope)
		}
	}
	return strings.Join(ordered, ","), nil
}

func parseAPIKeyPermissionSet(raw string) (apiKeyPermissionSet, error) {
	tokens := permissionTokens(raw)
	set := make(apiKeyPermissionSet)
	for _, token := range tokens {
		if expanded, ok := apiKeyScopeExpansions[token]; ok {
			for _, scope := range expanded {
				set[scope] = struct{}{}
			}
			continue
		}
		if _, ok := apiKeyScopeExpansions[tokenScopeWildcard(token)]; ok {
			for _, scope := range apiKeyScopeExpansions[tokenScopeWildcard(token)] {
				set[scope] = struct{}{}
			}
			continue
		}
		if !isSupportedAPIKeyScope(token) {
			return nil, errors.New("permissions must be read-only, read-write, or a comma-separated list of supported scopes")
		}
		set[token] = struct{}{}
	}
	return set, nil
}

func permissionTokens(raw string) []string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		raw = "read-write"
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return []string{"read-write"}
	}
	return out
}

func tokenScopeWildcard(token string) string {
	if !strings.Contains(token, ":") {
		return ""
	}
	resource, action, ok := strings.Cut(token, ":")
	if !ok {
		return ""
	}
	switch action {
	case "readwrite", "read-write", "rw", "write-read":
		return resource + ":*"
	default:
		return ""
	}
}

func isSupportedAPIKeyScope(scope string) bool {
	for _, candidate := range apiKeyScopeOrder {
		if scope == candidate {
			return true
		}
	}
	return false
}

func apiKeyPermissionAllowsRequest(permission, method, path string) (bool, string) {
	set, err := parseAPIKeyPermissionSet(permission)
	if err != nil {
		return false, "api key permissions are invalid"
	}

	required := requiredAPIKeyScopes(method, path)
	if len(required) == 0 {
		return false, "api key is not allowed for this endpoint"
	}

	for _, scope := range required {
		if _, ok := set[scope]; ok {
			return true, ""
		}
	}

	if normalized, err := normalizeAPIKeyPermissions(permission); err == nil && normalized == "read-only" && requestNeedsWriteScope(required) {
		return false, "api key is read-only"
	}
	return false, "api key does not include the required scope"
}

func requiredAPIKeyScopes(method, path string) []string {
	path = strings.ToLower(strings.TrimSpace(path))
	method = strings.ToUpper(strings.TrimSpace(method))

	switch {
	case path == "/api/server/status" || path == "/api/v1/server/status":
		return []string{"server:read"}
	case path == "/api/v1/server/config":
		if method == http.MethodGet {
			return []string{"server:read"}
		}
		return []string{"server:write"}
	case path == "/api/v1/server/config/validate" || path == "/api/v1/server/reload":
		return []string{"server:write"}
	case path == "/api/users" || path == "/api/v1/users":
		if method == http.MethodGet {
			return []string{"users:read"}
		}
		return []string{"users:write"}
	case path == "/api/users/import" || path == "/api/v1/users/import":
		return []string{"users:write"}
	case path == "/api/users/export" || path == "/api/v1/users/export":
		return []string{"users:read"}
	case path == "/api/apikeys" || path == "/api/v1/apikeys":
		if method == http.MethodGet {
			return []string{"apikeys:read"}
		}
		return []string{"apikeys:write"}
	case path == "/api/sessions" || path == "/api/v1/sessions":
		return []string{"sessions:read"}
	case strings.HasPrefix(path, "/api/sessions/") || strings.HasPrefix(path, "/api/v1/sessions/"):
		if method == http.MethodGet {
			return []string{"sessions:read"}
		}
		return []string{"sessions:write"}
	case path == "/api/files/list" || path == "/api/files/download":
		return []string{"files:read"}
	case path == "/api/files/mkdir" || path == "/api/files/delete" || path == "/api/files/rename" || path == "/api/files/upload":
		return []string{"files:write"}
	case strings.HasPrefix(path, "/api/v1/files/"):
		switch {
		case strings.HasSuffix(path, "/ls"), strings.HasSuffix(path, "/download"), strings.HasSuffix(path, "/stat"):
			return []string{"files:read"}
		case strings.HasSuffix(path, "/mkdir"), strings.HasSuffix(path, "/rm"), strings.HasSuffix(path, "/rename"), strings.HasSuffix(path, "/upload"):
			return []string{"files:write"}
		case strings.HasSuffix(path, "/share"):
			return []string{"share:write"}
		default:
			return nil
		}
	case path == "/api/share" || path == "/api/v1/share":
		if method == http.MethodGet {
			return []string{"share:read"}
		}
		return []string{"share:write"}
	case path == "/api/audit" || path == "/api/v1/audit/events" || path == "/api/audit/export" || path == "/api/v1/audit/export":
		return []string{"audit:read"}
	case path == "/api/transfers" || path == "/api/v1/transfers":
		return []string{"transfers:read"}
	default:
		return nil
	}
}

func requestNeedsWriteScope(required []string) bool {
	for _, scope := range required {
		if strings.HasSuffix(scope, ":write") {
			return true
		}
	}
	return false
}

func apiKeyScopeSetEquals(set apiKeyPermissionSet, scopes []string) bool {
	if len(set) != len(scopes) {
		return false
	}
	for _, scope := range scopes {
		if _, ok := set[scope]; !ok {
			return false
		}
	}
	return true
}

func apiKeySupportedScopeInfo() []APIKeyScopeInfo {
	out := make([]APIKeyScopeInfo, 0, len(apiKeyScopeOrder))
	for _, scope := range apiKeyScopeOrder {
		if info, ok := apiKeyScopeDescriptions[scope]; ok {
			out = append(out, info)
		}
	}
	return out
}

func apiKeyPresets() []APIKeyPermissionPreset {
	out := make([]APIKeyPermissionPreset, 0, len(apiKeyPermissionPresets))
	for _, preset := range apiKeyPermissionPresets {
		clone := preset
		clone.Scopes = append([]string(nil), preset.Scopes...)
		out = append(out, clone)
	}
	return out
}

func SupportedAPIKeyScopes() []APIKeyScopeInfo {
	return apiKeySupportedScopeInfo()
}

func APIKeyPresets() []APIKeyPermissionPreset {
	return apiKeyPresets()
}
