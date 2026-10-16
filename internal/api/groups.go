package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kervanserver/kervan/internal/audit"
	"github.com/kervanserver/kervan/internal/auth"
)

// userPolicyPatch carries the optional group, quota and permission fields
// accepted when creating or updating a user. Nil fields are left unchanged.
type userPolicyPatch struct {
	PrimaryGroup      *string               `json:"primary_group"`
	SecondaryGroups   *[]string             `json:"secondary_groups"`
	MaxStorage        *int64                `json:"max_storage"`
	MaxBandwidth      *int64                `json:"max_bandwidth"`
	MaxFiles          *int64                `json:"max_files"`
	CustomPermissions *bool                 `json:"custom_permissions"`
	Permissions       *auth.UserPermissions `json:"permissions"`
	// ExpiresAt is an RFC 3339 time; "" clears the expiry.
	ExpiresAt        *string   `json:"expires_at"`
	AllowedIPs       *[]string `json:"allowed_ips"`
	AllowedProtocols *[]string `json:"allowed_protocols"`
	MaxSessions      *int      `json:"max_sessions"`
}

func (p userPolicyPatch) set() bool {
	return p.PrimaryGroup != nil || p.SecondaryGroups != nil || p.MaxStorage != nil || p.MaxBandwidth != nil || p.MaxFiles != nil ||
		p.CustomPermissions != nil || p.Permissions != nil ||
		p.ExpiresAt != nil || p.AllowedIPs != nil || p.AllowedProtocols != nil || p.MaxSessions != nil
}

// Applying a userPolicyPatch is two steps: resolveUserPolicyGroups, then
// applyResolvedUserPolicy. Group references set through the API must name
// existing groups.

// resolveUserPolicyGroups replaces the patch's group references with the
// canonical names of existing groups, deduplicating secondary groups. It
// reads the store, so inside a user transaction it must run beforehand and
// the result be applied with applyResolvedUserPolicy.
func (s *Server) resolveUserPolicyGroups(p userPolicyPatch) (userPolicyPatch, error) {
	resolve := func(name string) (string, error) {
		name = strings.TrimSpace(name)
		if name == "" {
			return "", nil
		}
		g, err := s.groups.GetByName(name)
		if err != nil {
			return "", err
		}
		if g == nil {
			return "", fmt.Errorf("group %q does not exist", name)
		}
		return g.Name, nil
	}
	if p.PrimaryGroup != nil {
		name, err := resolve(*p.PrimaryGroup)
		if err != nil {
			return p, err
		}
		p.PrimaryGroup = &name
	}
	if p.SecondaryGroups != nil {
		out := make([]string, 0, len(*p.SecondaryGroups))
		seen := map[string]bool{}
		for _, raw := range *p.SecondaryGroups {
			name, err := resolve(raw)
			if err != nil {
				return p, err
			}
			if name != "" && !seen[strings.ToLower(name)] {
				seen[strings.ToLower(name)] = true
				out = append(out, name)
			}
		}
		p.SecondaryGroups = &out
	}
	return p, nil
}

// applyResolvedUserPolicy validates and applies a patch whose groups were
// resolved by resolveUserPolicyGroups. It does not touch the store, so it is
// safe inside a UserRepository.Mutate callback.
func applyResolvedUserPolicy(u *auth.User, p userPolicyPatch) error {
	if p.PrimaryGroup != nil {
		u.PrimaryGroup = *p.PrimaryGroup
	}
	if p.SecondaryGroups != nil {
		u.SecondaryGrps = append([]string{}, *p.SecondaryGroups...)
	}
	if p.MaxStorage != nil {
		if *p.MaxStorage < -1 {
			return errors.New("max_storage must be -1 (unlimited), 0 (inherit) or a byte count")
		}
		u.MaxStorage = *p.MaxStorage
	}
	if p.MaxBandwidth != nil {
		if *p.MaxBandwidth < -1 {
			return errors.New("max_bandwidth must be -1 (unlimited), 0 (inherit) or bytes per second")
		}
		u.MaxBandwidth = *p.MaxBandwidth
	}
	if p.MaxFiles != nil {
		if *p.MaxFiles < -1 {
			return errors.New("max_files must be -1 (unlimited), 0 (inherit) or a file count")
		}
		u.MaxFiles = *p.MaxFiles
	}
	if p.CustomPermissions != nil {
		u.CustomPermissions = *p.CustomPermissions
	}
	if p.Permissions != nil {
		u.Permissions = *p.Permissions
	}
	if p.ExpiresAt != nil {
		raw := strings.TrimSpace(*p.ExpiresAt)
		if raw == "" {
			u.ExpiresAt = nil
		} else {
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return errors.New("expires_at must be an RFC 3339 time, e.g. 2026-12-31T23:59:59Z, or empty to clear")
			}
			t = t.UTC()
			u.ExpiresAt = &t
		}
	}
	ips, protocols, maxSessions := u.AllowedIPs, u.AllowedProtocols, u.MaxSessions
	if p.AllowedIPs != nil {
		ips = *p.AllowedIPs
	}
	if p.AllowedProtocols != nil {
		protocols = *p.AllowedProtocols
	}
	if p.MaxSessions != nil {
		maxSessions = *p.MaxSessions
	}
	ips, protocols, err := auth.NormalizeAccessPolicy(ips, protocols, maxSessions)
	if err != nil {
		return err
	}
	u.AllowedIPs, u.AllowedProtocols, u.MaxSessions = ips, protocols, maxSessions
	return nil
}

type userResponse struct {
	ID                string               `json:"id"`
	Username          string               `json:"username"`
	Type              string               `json:"type"`
	AuthProvider      string               `json:"auth_provider"`
	Enabled           bool                 `json:"enabled"`
	HomeDir           string               `json:"home_dir"`
	UpdatedAt         time.Time            `json:"updated_at"`
	PrimaryGroup      string               `json:"primary_group"`
	SecondaryGroups   []string             `json:"secondary_groups"`
	MaxStorage        int64                `json:"max_storage"`
	MaxBandwidth      int64                `json:"max_bandwidth"`
	MaxFiles          int64                `json:"max_files"`
	CustomPermissions bool                 `json:"custom_permissions"`
	Permissions       auth.UserPermissions `json:"permissions"`
	Effective         effectivePolicyJSON  `json:"effective"`
	AuthorizedKeys    []auth.KeyInfo       `json:"authorized_keys"`
	ExpiresAt         *time.Time           `json:"expires_at"`
	Expired           bool                 `json:"expired"`
	AllowedIPs        []string             `json:"allowed_ips"`
	AllowedProtocols  []string             `json:"allowed_protocols"`
	MaxSessions       int                  `json:"max_sessions"`
}

type effectivePolicyJSON struct {
	Permissions auth.UserPermissions `json:"permissions"`
	// MaxStorage is the enforced quota in bytes; 0 means unlimited.
	MaxStorage int64 `json:"max_storage"`
	// MaxBandwidth is the enforced rate limit in bytes/s; 0 means unlimited.
	MaxBandwidth int64 `json:"max_bandwidth"`
	// MaxFiles is the enforced file-count quota; 0 means unlimited.
	MaxFiles int64  `json:"max_files"`
	Group    string `json:"group,omitempty"`
}

func (s *Server) userResponse(u *auth.User) userResponse {
	secondary := u.SecondaryGrps
	if secondary == nil {
		secondary = []string{}
	}
	out := userResponse{
		ID:                u.ID,
		Username:          u.Username,
		Type:              string(u.Type),
		AuthProvider:      u.AuthProvider,
		Enabled:           u.Enabled,
		HomeDir:           u.HomeDir,
		UpdatedAt:         u.UpdatedAt,
		PrimaryGroup:      u.PrimaryGroup,
		SecondaryGroups:   secondary,
		MaxStorage:        u.MaxStorage,
		MaxBandwidth:      u.MaxBandwidth,
		MaxFiles:          u.MaxFiles,
		CustomPermissions: u.CustomPermissions,
		Permissions:       u.Permissions,
		AuthorizedKeys:    auth.DescribeAuthorizedKeys(u.AuthorizedKeys),
		ExpiresAt:         u.ExpiresAt,
		Expired:           u.Expired(time.Now()),
		AllowedIPs:        nonNil(u.AllowedIPs),
		AllowedProtocols:  nonNil(u.AllowedProtocols),
		MaxSessions:       u.MaxSessions,
	}
	cfg := s.currentConfig()
	defaults := auth.PolicyDefaults{MaxStorage: cfg.DefaultMaxStorage, MaxBandwidth: cfg.DefaultUserRate, MaxFiles: cfg.DefaultMaxFiles}
	if policy, err := s.groups.PolicyFor(u, defaults); err == nil {
		out.Effective = effectivePolicyJSON{Permissions: policy.Permissions, MaxStorage: policy.MaxStorage, MaxBandwidth: policy.MaxBandwidth, MaxFiles: policy.MaxFiles}
		// Mirrors buildUserFS: no quotas when disabled or for admins.
		if !cfg.QuotaEnabled || u.Type == auth.UserTypeAdmin {
			out.Effective.MaxStorage, out.Effective.MaxFiles = 0, 0
		}
		if policy.Group != nil {
			out.Effective.Group = policy.Group.Name
		}
	}
	return out
}

type groupResponse struct {
	*auth.Group
	MemberCount int `json:"member_count"`
}

func (s *Server) handleGroups(w http.ResponseWriter, r *http.Request) {
	if !s.isAdminUser(currentUser(r)) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		groups, err := s.groups.List()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list groups failed"})
			return
		}
		out := make([]groupResponse, 0, len(groups))
		for _, g := range groups {
			members, _ := s.groups.Members(g.Name)
			out = append(out, groupResponse{Group: g, MemberCount: len(members)})
		}
		writeJSON(w, http.StatusOK, map[string]any{"groups": out})
	case http.MethodPost:
		var req struct {
			Name         string                `json:"name"`
			Description  string                `json:"description"`
			Permissions  *auth.UserPermissions `json:"permissions"`
			MaxStorage   int64                 `json:"max_storage"`
			MaxBandwidth int64                 `json:"max_bandwidth"`
			MaxFiles     int64                 `json:"max_files"`
		}
		if err := decodeJSONBody(w, r, &req, false); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		g := &auth.Group{
			Name:         req.Name,
			Description:  strings.TrimSpace(req.Description),
			Permissions:  auth.DefaultUserPermissions(),
			MaxStorage:   req.MaxStorage,
			MaxBandwidth: req.MaxBandwidth,
			MaxFiles:     req.MaxFiles,
		}
		if req.Permissions != nil {
			g.Permissions = *req.Permissions
		}
		if err := s.groups.Create(g); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		s.emitAudit(r, audit.EventAdminGroupCreate, "", "ok", "group created", map[string]string{"group": g.Name})
		writeJSON(w, http.StatusCreated, groupResponse{Group: g})
	case http.MethodPut:
		var req struct {
			ID           string                `json:"id"`
			Name         *string               `json:"name"`
			Description  *string               `json:"description"`
			Permissions  *auth.UserPermissions `json:"permissions"`
			MaxStorage   *int64                `json:"max_storage"`
			MaxBandwidth *int64                `json:"max_bandwidth"`
			MaxFiles     *int64                `json:"max_files"`
		}
		if err := decodeJSONBody(w, r, &req, false); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		g, err := s.groups.GetByID(strings.TrimSpace(req.ID))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if g == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "group not found"})
			return
		}
		if req.Name != nil {
			g.Name = *req.Name
		}
		if req.Description != nil {
			g.Description = strings.TrimSpace(*req.Description)
		}
		if req.Permissions != nil {
			g.Permissions = *req.Permissions
		}
		if req.MaxStorage != nil {
			g.MaxStorage = *req.MaxStorage
		}
		if req.MaxBandwidth != nil {
			g.MaxBandwidth = *req.MaxBandwidth
		}
		if req.MaxFiles != nil {
			g.MaxFiles = *req.MaxFiles
		}
		if err := s.groups.Update(g); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		s.emitAudit(r, audit.EventAdminGroupUpdate, "", "ok", "group updated", map[string]string{"group": g.Name, "group_id": g.ID})
		members, _ := s.groups.Members(g.Name)
		writeJSON(w, http.StatusOK, groupResponse{Group: g, MemberCount: len(members)})
	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		force := r.URL.Query().Get("force") == "true"
		switch err := s.groups.Delete(id, force); {
		case err == nil:
			s.emitAudit(r, audit.EventAdminGroupDelete, "", "ok", "group deleted", map[string]string{"group_id": id, "force": strconv.FormatBool(force)})
			writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
		case errors.Is(err, auth.ErrGroupNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "group not found"})
		case errors.Is(err, auth.ErrGroupInUse):
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
