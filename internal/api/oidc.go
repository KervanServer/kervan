package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/oidc"
)

// OIDCSettings enables WebUI sign-in through an OpenID provider.
type OIDCSettings struct {
	Provider      *oidc.Provider
	ButtonLabel   string
	UsernameClaim string
	GroupsClaim   string
	AllowedGroups []string
	AdminGroups   []string
	GroupMapping  map[string]string
	AutoCreate    bool
	HomeDir       string
	// SecureCookie marks the login-state cookie Secure (https redirect URL).
	SecureCookie bool
}

const (
	oidcCookieName = "kervan_oidc"
	oidcCookiePath = "/api/v1/auth/oidc/"
	oidcStateTTL   = 10 * time.Minute
	oidcGrantTTL   = time.Minute
	maxOIDCGrants  = 1024
)

type oidcGrant struct {
	token   string
	user    *auth.User
	expires time.Time
}

// oidcGrants holds one-time codes that hand a finished sign-in to the SPA,
// so the session token never appears in a URL.
type oidcGrants struct {
	mu     sync.Mutex
	grants map[string]oidcGrant
}

func (g *oidcGrants) put(grant oidcGrant) (string, error) {
	code, err := randomURLToken()
	if err != nil {
		return "", err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.grants == nil {
		g.grants = make(map[string]oidcGrant)
	}
	now := time.Now()
	for k, v := range g.grants {
		if now.After(v.expires) {
			delete(g.grants, k)
		}
	}
	if len(g.grants) >= maxOIDCGrants {
		return "", errors.New("too many pending sign-ins")
	}
	g.grants[code] = grant
	return code, nil
}

func (g *oidcGrants) take(code string) (oidcGrant, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	grant, ok := g.grants[code]
	delete(g.grants, code)
	if !ok || time.Now().After(grant.expires) {
		return oidcGrant{}, false
	}
	return grant, true
}

func randomURLToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

type oidcCookieState struct {
	oidc.AuthRequest
	Expires int64 `json:"e"`
}

func (s *Server) oidcCookieKey() []byte {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte("kervan oidc login state"))
	return m.Sum(nil)
}

func (s *Server) encodeOIDCState(st oidcCookieState) (string, error) {
	payload, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	m := hmac.New(sha256.New, s.oidcCookieKey())
	m.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil)), nil
}

func (s *Server) decodeOIDCState(raw string) (oidcCookieState, error) {
	var st oidcCookieState
	payloadB64, sigB64, ok := strings.Cut(raw, ".")
	if !ok {
		return st, errors.New("malformed state cookie")
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(payloadB64)
	sig, err2 := base64.RawURLEncoding.DecodeString(sigB64)
	if err1 != nil || err2 != nil {
		return st, errors.New("malformed state cookie")
	}
	m := hmac.New(sha256.New, s.oidcCookieKey())
	m.Write(payload)
	if !hmac.Equal(sig, m.Sum(nil)) {
		return st, errors.New("state cookie signature mismatch")
	}
	if err := json.Unmarshal(payload, &st); err != nil {
		return st, err
	}
	if time.Now().Unix() > st.Expires {
		return st, errors.New("sign-in attempt expired")
	}
	return st, nil
}

func (s *Server) setOIDCCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     oidcCookieName,
		Value:    value,
		Path:     oidcCookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.oidc != nil && s.oidc.SecureCookie,
		// Lax: the callback is a top-level GET navigation from the provider.
		SameSite: http.SameSiteLaxMode,
	})
}

// handleAuthMethods tells the login screen which sign-in options exist.
func (s *Server) handleAuthMethods(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	oidcInfo := map[string]any{"enabled": false}
	if s.oidc != nil {
		oidcInfo = map[string]any{"enabled": true, "label": s.oidc.ButtonLabel, "login_url": "/api/v1/auth/oidc/login"}
	}
	writeJSON(w, http.StatusOK, map[string]any{"password": true, "oidc": oidcInfo})
}

func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		http.NotFound(w, r)
		return
	}
	req, err := oidc.NewAuthRequest()
	if err != nil {
		s.oidcFail(w, r, "server_error", err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	target, err := s.oidc.Provider.AuthCodeURL(ctx, req)
	if err != nil {
		s.oidcFail(w, r, "provider_unavailable", err)
		return
	}
	cookie, err := s.encodeOIDCState(oidcCookieState{AuthRequest: req, Expires: time.Now().Add(oidcStateTTL).Unix()})
	if err != nil {
		s.oidcFail(w, r, "server_error", err)
		return
	}
	s.setOIDCCookie(w, cookie, int(oidcStateTTL.Seconds()))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target, http.StatusFound)
}

// oidcFail ends the browser flow on the login screen with a short error
// code; details go to the log only.
func (s *Server) oidcFail(w http.ResponseWriter, r *http.Request, code string, err error) {
	if s.logger != nil {
		s.logger.Warn("oidc sign-in failed", "reason", code, "error", err, "remote_addr", r.RemoteAddr)
	}
	s.setOIDCCookie(w, "", -1)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/?oidc_error="+url.QueryEscape(code), http.StatusFound)
}

func sanitizeProviderError(code string) string {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 64 {
		return "provider_error"
	}
	for _, r := range code {
		if !(r == '_' || (r >= 'a' && r <= 'z')) {
			return "provider_error"
		}
	}
	return code
}

func validExternalUsername(name string) bool {
	if name == "" || len(name) > 128 || strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// usernameFromClaims tries each comma-separated claim name in order. An
// "email" claim is only used when the provider marks it verified (or does
// not say), since an unverified address could impersonate another user.
func usernameFromClaims(claims oidc.Claims, claimList string) string {
	for _, name := range strings.Split(claimList, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if name == "email" && claims["email_verified"] != nil && !claims.Bool("email_verified") {
			continue
		}
		if value := strings.TrimSpace(claims.String(name)); validExternalUsername(value) {
			return value
		}
	}
	return ""
}

func intersects(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	if providerErr := q.Get("error"); providerErr != "" {
		s.oidcFail(w, r, sanitizeProviderError(providerErr), errors.New(q.Get("error_description")))
		return
	}
	cookie, err := r.Cookie(oidcCookieName)
	if err != nil {
		s.oidcFail(w, r, "invalid_state", errors.New("missing state cookie"))
		return
	}
	state, err := s.decodeOIDCState(cookie.Value)
	if err != nil {
		s.oidcFail(w, r, "invalid_state", err)
		return
	}
	if subtle.ConstantTimeCompare([]byte(state.State), []byte(q.Get("state"))) != 1 {
		s.oidcFail(w, r, "invalid_state", errors.New("state mismatch"))
		return
	}
	s.setOIDCCookie(w, "", -1)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	claims, err := s.oidc.Provider.Exchange(ctx, q.Get("code"), state.AuthRequest)
	if err != nil {
		s.oidcFail(w, r, "token_error", err)
		return
	}

	settings := s.oidc
	username := usernameFromClaims(claims, settings.UsernameClaim)
	if username == "" {
		names := make([]string, 0, len(claims))
		for name := range claims {
			names = append(names, name)
		}
		sort.Strings(names)
		s.oidcFail(w, r, "missing_username", fmt.Errorf("none of the claims %q holds a usable username (token claims: %s)", settings.UsernameClaim, strings.Join(names, ", ")))
		return
	}
	providerGroups := claims.Strings(settings.GroupsClaim)
	if len(settings.AllowedGroups) > 0 && !intersects(providerGroups, settings.AllowedGroups) {
		s.oidcFail(w, r, "not_allowed", errors.New(username+" is not in an allowed group"))
		return
	}

	identity := auth.ExternalIdentity{
		Provider: auth.AuthProviderOIDC,
		Subject:  claims.String("sub"),
		Username: username,
		HomeDir:  strings.ReplaceAll(settings.HomeDir, "{username}", username),
	}
	if claims.Bool("email_verified") || claims["email_verified"] == nil {
		identity.Email = claims.String("email")
	}
	if len(settings.AdminGroups) > 0 {
		isAdmin := intersects(providerGroups, settings.AdminGroups)
		identity.Admin = &isAdmin
	}
	// Group memberships follow the provider only when it sends the claim,
	// so manually assigned groups survive providers without group claims.
	if _, present := claims[settings.GroupsClaim]; present {
		identity.SyncGroups = true
		seen := map[string]bool{}
		for _, providerGroup := range providerGroups {
			name := providerGroup
			if mapped, ok := settings.GroupMapping[providerGroup]; ok {
				name = mapped
			}
			g, err := s.groups.GetByName(name)
			if err != nil || g == nil || seen[strings.ToLower(g.Name)] {
				continue
			}
			seen[strings.ToLower(g.Name)] = true
			if identity.PrimaryGroup == "" {
				identity.PrimaryGroup = g.Name
			} else {
				identity.SecondaryGroups = append(identity.SecondaryGroups, g.Name)
			}
		}
	}

	user, err := s.auth.ProvisionExternalUser(identity, settings.AutoCreate)
	switch {
	case errors.Is(err, auth.ErrAccountConflict):
		s.oidcFail(w, r, "account_conflict", err)
		return
	case errors.Is(err, auth.ErrNotProvisioned):
		s.oidcFail(w, r, "not_provisioned", err)
		return
	case errors.Is(err, auth.ErrUserDisabled):
		s.oidcFail(w, r, "account_disabled", err)
		return
	case err != nil:
		s.oidcFail(w, r, "server_error", err)
		return
	}

	token, err := signToken(s.secret, user.Username, s.currentConfig().SessionTimeout)
	if err != nil {
		s.oidcFail(w, r, "server_error", err)
		return
	}
	code, err := s.oidcGrants.put(oidcGrant{token: token, user: user, expires: time.Now().Add(oidcGrantTTL)})
	if err != nil {
		s.oidcFail(w, r, "server_error", err)
		return
	}
	_ = s.auth.RecordSuccessfulLogin(user.ID)
	if s.logger != nil {
		s.logger.Info("oidc sign-in", "user", user.Username, "type", user.Type, "group", user.PrimaryGroup, "remote_addr", r.RemoteAddr)
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/?oidc_code="+url.QueryEscape(code), http.StatusFound)
}

// handleOIDCExchange trades a one-time code for the session token.
func (s *Server) handleOIDCExchange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSONBody(w, r, &req, false); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	grant, ok := s.oidcGrants.take(strings.TrimSpace(req.Code))
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sign-in code is invalid or expired"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"token": grant.token,
		"user": map[string]any{
			"id":       grant.user.ID,
			"username": grant.user.Username,
			"type":     grant.user.Type,
		},
	})
}
