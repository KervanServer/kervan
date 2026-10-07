// Package oidc implements the OpenID Connect relying-party pieces Kervan
// needs for WebUI sign-in: discovery, the authorization-code flow with PKCE,
// and ID token verification against the provider's JWKS. It depends only on
// the standard library.
package oidc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Config describes the relying party.
type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
	// HTTPClient is used for discovery, JWKS and token requests; nil uses a
	// client with a 10s timeout.
	HTTPClient *http.Client
	// Now is overridable for tests.
	Now func() time.Time
}

// Metadata is the subset of the discovery document Kervan uses.
type Metadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	UserinfoEndpoint      string `json:"userinfo_endpoint,omitempty"`
}

// Provider is a discovered OpenID provider. Discovery is lazy, so the server
// starts even while the provider is unreachable.
type Provider struct {
	cfg    Config
	client *http.Client

	mu       sync.Mutex
	meta     *Metadata
	keys     *keySet
	keysAt   time.Time
	lastFail time.Time
}

const (
	maxResponseBytes = 1 << 20
	clockSkew        = time.Minute
	// minKeyRefresh rate-limits JWKS refetches triggered by unknown key IDs.
	minKeyRefresh = 30 * time.Second
	keyCacheTTL   = time.Hour
)

func NewProvider(cfg Config) (*Provider, error) {
	if strings.TrimSpace(cfg.Issuer) == "" || strings.TrimSpace(cfg.ClientID) == "" || strings.TrimSpace(cfg.RedirectURL) == "" {
		return nil, errors.New("oidc: issuer, client_id and redirect_url are required")
	}
	cfg.Issuer = strings.TrimRight(strings.TrimSpace(cfg.Issuer), "/")
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "profile", "email"}
	}
	hasOpenID := false
	for _, s := range cfg.Scopes {
		if s == "openid" {
			hasOpenID = true
		}
	}
	if !hasOpenID {
		cfg.Scopes = append([]string{"openid"}, cfg.Scopes...)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Provider{cfg: cfg, client: client}, nil
}

func (p *Provider) getJSON(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", rawURL, resp.StatusCode)
	}
	return json.Unmarshal(body, out)
}

// Metadata returns the discovery document, fetching it on first use.
func (p *Provider) Metadata(ctx context.Context) (*Metadata, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.meta != nil {
		return p.meta, nil
	}
	var meta Metadata
	if err := p.getJSON(ctx, p.cfg.Issuer+"/.well-known/openid-configuration", &meta); err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	// OpenID Connect Discovery 1.0 §4.3: the issuer must match exactly.
	if strings.TrimRight(meta.Issuer, "/") != p.cfg.Issuer {
		return nil, fmt.Errorf("oidc discovery: issuer %q does not match configured %q", meta.Issuer, p.cfg.Issuer)
	}
	if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" || meta.JWKSURI == "" {
		return nil, errors.New("oidc discovery: authorization_endpoint, token_endpoint and jwks_uri are required")
	}
	meta.Issuer = p.cfg.Issuer
	p.meta = &meta
	return p.meta, nil
}

// AuthRequest carries the per-login secrets that must survive the redirect
// round trip (Kervan keeps them in a signed, HttpOnly cookie).
type AuthRequest struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NewAuthRequest generates a fresh state, nonce and PKCE verifier.
func NewAuthRequest() (AuthRequest, error) {
	var r AuthRequest
	var err error
	if r.State, err = randomToken(); err != nil {
		return r, err
	}
	if r.Nonce, err = randomToken(); err != nil {
		return r, err
	}
	if r.Verifier, err = randomToken(); err != nil {
		return r, err
	}
	return r, nil
}

// pkceChallenge is the S256 code challenge for verifier (RFC 7636).
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// AuthCodeURL is the provider URL the browser is sent to.
func (p *Provider) AuthCodeURL(ctx context.Context, req AuthRequest) (string, error) {
	meta, err := p.Metadata(ctx)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(meta.AuthorizationEndpoint)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", p.cfg.ClientID)
	q.Set("redirect_uri", p.cfg.RedirectURL)
	q.Set("scope", strings.Join(p.cfg.Scopes, " "))
	q.Set("state", req.State)
	q.Set("nonce", req.Nonce)
	q.Set("code_challenge", pkceChallenge(req.Verifier))
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

type tokenResponse struct {
	IDToken          string `json:"id_token"`
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// Exchange redeems an authorization code and returns the verified ID token
// claims.
func (p *Provider) Exchange(ctx context.Context, code string, req AuthRequest) (Claims, error) {
	meta, err := p.Metadata(ctx)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", p.cfg.RedirectURL)
	form.Set("code_verifier", req.Verifier)
	form.Set("client_id", p.cfg.ClientID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("Accept", "application/json")
	if p.cfg.ClientSecret != "" {
		// client_secret_basic (RFC 6749 §2.3.1): form-encode, then basic auth.
		httpReq.SetBasicAuth(url.QueryEscape(p.cfg.ClientID), url.QueryEscape(p.cfg.ClientSecret))
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("oidc token request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	var tok tokenResponse
	_ = json.Unmarshal(body, &tok)
	if resp.StatusCode != http.StatusOK || tok.Error != "" {
		if tok.Error != "" {
			return nil, fmt.Errorf("oidc token request: %s %s", tok.Error, tok.ErrorDescription)
		}
		return nil, fmt.Errorf("oidc token request: HTTP %d", resp.StatusCode)
	}
	if tok.IDToken == "" {
		return nil, errors.New("oidc token response has no id_token")
	}
	return p.VerifyIDToken(ctx, tok.IDToken, req.Nonce)
}

// keysFor returns the provider's keys, refetching when kid is unknown (key
// rotation) but at most once per minKeyRefresh.
func (p *Provider) keysFor(ctx context.Context, kid string) (*keySet, error) {
	meta, err := p.Metadata(ctx)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.cfg.Now()
	if p.keys != nil {
		fresh := now.Sub(p.keysAt) < keyCacheTTL
		if fresh && (kid == "" || p.keys.has(kid)) {
			return p.keys, nil
		}
		// An unknown kid triggers a refetch (key rotation), but tokens with
		// made-up kids must not make every request hit the provider.
		if now.Sub(p.keysAt) < minKeyRefresh || now.Sub(p.lastFail) < minKeyRefresh {
			return p.keys, nil
		}
	}
	var raw jwks
	if err := p.getJSON(ctx, meta.JWKSURI, &raw); err != nil {
		p.lastFail = now
		if p.keys != nil {
			return p.keys, nil
		}
		return nil, fmt.Errorf("oidc jwks: %w", err)
	}
	set, err := parseJWKS(raw)
	if err != nil {
		return nil, err
	}
	p.keys, p.keysAt = set, now
	return set, nil
}

// VerifyIDToken checks the token's signature and its iss, aud, azp, exp,
// iat/nbf and nonce claims.
func (p *Provider) VerifyIDToken(ctx context.Context, raw, nonce string) (Claims, error) {
	tok, err := parseJWT(raw)
	if err != nil {
		return nil, err
	}
	keys, err := p.keysFor(ctx, tok.header.KeyID)
	if err != nil {
		return nil, err
	}
	if err := keys.verify(tok); err != nil {
		return nil, err
	}
	claims := tok.claims
	now := p.cfg.Now()
	if claims.String("iss") != p.cfg.Issuer {
		return nil, fmt.Errorf("oidc: unexpected issuer %q", claims.String("iss"))
	}
	aud := claims.Strings("aud")
	if !contains(aud, p.cfg.ClientID) {
		return nil, errors.New("oidc: token audience does not include this client")
	}
	if len(aud) > 1 && claims.String("azp") != p.cfg.ClientID {
		return nil, errors.New("oidc: authorized party mismatch")
	}
	exp, ok := claims.Time("exp")
	if !ok || now.After(exp.Add(clockSkew)) {
		return nil, errors.New("oidc: token expired")
	}
	if iat, ok := claims.Time("iat"); ok && iat.After(now.Add(clockSkew)) {
		return nil, errors.New("oidc: token issued in the future")
	}
	if nbf, ok := claims.Time("nbf"); ok && nbf.After(now.Add(clockSkew)) {
		return nil, errors.New("oidc: token not yet valid")
	}
	if nonce != "" && claims.String("nonce") != nonce {
		return nil, errors.New("oidc: nonce mismatch")
	}
	if claims.String("sub") == "" {
		return nil, errors.New("oidc: token has no subject")
	}
	return claims, nil
}

// Claims are decoded ID token claims.
type Claims map[string]any

func (c Claims) String(name string) string {
	s, _ := c[name].(string)
	return s
}

// Strings reads a claim that may be a string or a list of strings.
func (c Claims) Strings(name string) []string {
	switch v := c[name].(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func (c Claims) Bool(name string) bool {
	switch v := c[name].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	default:
		return false
	}
}

func (c Claims) Time(name string) (time.Time, bool) {
	switch v := c[name].(type) {
	case float64:
		return time.Unix(int64(v), 0), true
	case json.Number:
		n, err := v.Int64()
		return time.Unix(n, 0), err == nil
	default:
		return time.Time{}, false
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
