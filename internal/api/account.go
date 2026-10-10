package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/kervanserver/kervan/internal/auth"
)

// requireSessionUser resolves the signed-in user for self-service account
// endpoints. API keys are refused: changing credentials needs an
// interactive session.
func (s *Server) requireSessionUser(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
	if r.Header.Get("X-Auth-Method") != "bearer" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "account changes require a signed-in session, not an API key"})
		return nil, false
	}
	user, err := s.users.GetByUsername(currentUser(r))
	if err != nil || user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "user not found"})
		return nil, false
	}
	return user, true
}

func passwordManaged(u *auth.User) bool {
	return u.AuthProvider == "" || strings.EqualFold(u.AuthProvider, auth.AuthProviderLocal)
}

type accountResponse struct {
	userResponse
	Email              string         `json:"email,omitempty"`
	PasswordChangeable bool           `json:"password_changeable"`
	AuthorizedKeys     []auth.KeyInfo `json:"authorized_keys"`
}

func (s *Server) accountResponse(u *auth.User) accountResponse {
	return accountResponse{
		userResponse:       s.userResponse(u),
		Email:              u.Email,
		PasswordChangeable: passwordManaged(u),
		AuthorizedKeys:     auth.DescribeAuthorizedKeys(u.AuthorizedKeys),
	}
}

// handleAccount returns the signed-in user's own profile.
func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	user, ok := s.requireSessionUser(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.accountResponse(user))
}

// handleAccountPassword changes the signed-in user's password. Other
// sessions are revoked; the response carries a fresh token for this one.
func (s *Server) handleAccountPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	user, ok := s.requireSessionUser(w, r)
	if !ok {
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeJSONBody(w, r, &req, false); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	switch err := s.auth.ChangePassword(user.Username, req.CurrentPassword, req.NewPassword); {
	case err == nil:
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "current password is incorrect"})
		return
	case errors.Is(err, auth.ErrPasswordNotManaged):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// sessions_valid_after is the change time truncated to the second, so a
	// token issued now (iat = now) stays valid while older ones are revoked.
	token, err := signToken(s.secret, user.Username, s.currentConfig().SessionTimeout)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token generation failed"})
		return
	}
	if s.logger != nil {
		s.logger.Info("password changed", "user", user.Username, "remote_addr", r.RemoteAddr)
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "password changed", "token": token})
}

// handleAccountKeys replaces the signed-in user's SSH authorized keys.
func (s *Server) handleAccountKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	user, ok := s.requireSessionUser(w, r)
	if !ok {
		return
	}
	var req struct {
		AuthorizedKeys []string `json:"authorized_keys"`
	}
	if err := decodeJSONBody(w, r, &req, false); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	keys, err := auth.NormalizeAuthorizedKeys(req.AuthorizedKeys)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	user.AuthorizedKeys = keys
	if err := s.users.Update(user); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.accountResponse(user))
}
