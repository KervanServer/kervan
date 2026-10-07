package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
)

// userPolicyPatch carries the optional group, quota and permission fields
// accepted when creating or updating a user. Nil fields are left unchanged.
type userPolicyPatch struct {
	PrimaryGroup      *string               `json:"primary_group"`
	SecondaryGroups   *[]string             `json:"secondary_groups"`
	MaxStorage        *int64                `json:"max_storage"`
	CustomPermissions *bool                 `json:"custom_permissions"`
	Permissions       *auth.UserPermissions `json:"permissions"`
}

func (p userPolicyPatch) set() bool {
	return p.PrimaryGroup != nil || p.SecondaryGroups != nil || p.MaxStorage != nil ||
		p.CustomPermissions != nil || p.Permissions != nil
}

// applyUserPolicyPatch validates the patch and applies it to u. Group
// references set through the API must name existing groups.
func (s *Server) applyUserPolicyPatch(u *auth.User, p userPolicyPatch) error {
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
			return err
		}
		u.PrimaryGroup = name
	}
	if p.SecondaryGroups != nil {
		out := make([]string, 0, len(*p.SecondaryGroups))
		seen := map[string]bool{}
		for _, raw := range *p.SecondaryGroups {
			name, err := resolve(raw)
			if err != nil {
				return err
			}
			if name != "" && !seen[strings.ToLower(name)] {
				seen[strings.ToLower(name)] = true
				out = append(out, name)
			}
		}
		u.SecondaryGrps = out
	}
	if p.MaxStorage != nil {
		if *p.MaxStorage < -1 {
			return errors.New("max_storage must be -1 (unlimited), 0 (inherit) or a byte count")
		}
		u.MaxStorage = *p.MaxStorage
	}
	if p.CustomPermissions != nil {
		u.CustomPermissions = *p.CustomPermissions
	}
	if p.Permissions != nil {
		u.Permissions = *p.Permissions
	}
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
	CustomPermissions bool                 `json:"custom_permissions"`
	Permissions       auth.UserPermissions `json:"permissions"`
	Effective         effectivePolicyJSON  `json:"effective"`
}

type effectivePolicyJSON struct {
	Permissions auth.UserPermissions `json:"permissions"`
	// MaxStorage is the enforced quota in bytes; 0 means unlimited.
	MaxStorage int64  `json:"max_storage"`
	Group      string `json:"group,omitempty"`
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
		CustomPermissions: u.CustomPermissions,
		Permissions:       u.Permissions,
	}
	cfg := s.currentConfig()
	if policy, err := s.groups.PolicyFor(u, cfg.DefaultMaxStorage); err == nil {
		out.Effective = effectivePolicyJSON{Permissions: policy.Permissions, MaxStorage: policy.MaxStorage}
		// Mirrors buildUserFS: no quota when disabled or for admins.
		if !cfg.QuotaEnabled || u.Type == auth.UserTypeAdmin {
			out.Effective.MaxStorage = 0
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
			Name        string                `json:"name"`
			Description string                `json:"description"`
			Permissions *auth.UserPermissions `json:"permissions"`
			MaxStorage  int64                 `json:"max_storage"`
		}
		if err := decodeJSONBody(w, r, &req, false); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		g := &auth.Group{
			Name:        req.Name,
			Description: strings.TrimSpace(req.Description),
			Permissions: auth.DefaultUserPermissions(),
			MaxStorage:  req.MaxStorage,
		}
		if req.Permissions != nil {
			g.Permissions = *req.Permissions
		}
		if err := s.groups.Create(g); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, groupResponse{Group: g})
	case http.MethodPut:
		var req struct {
			ID          string                `json:"id"`
			Name        *string               `json:"name"`
			Description *string               `json:"description"`
			Permissions *auth.UserPermissions `json:"permissions"`
			MaxStorage  *int64                `json:"max_storage"`
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
		if err := s.groups.Update(g); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		members, _ := s.groups.Members(g.Name)
		writeJSON(w, http.StatusOK, groupResponse{Group: g, MemberCount: len(members)})
	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		force := r.URL.Query().Get("force") == "true"
		switch err := s.groups.Delete(id, force); {
		case err == nil:
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
