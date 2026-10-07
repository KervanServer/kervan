// Package oidctest provides an in-process OpenID provider for tests: it
// serves discovery and JWKS, auto-approves authorization requests and
// enforces PKCE and client authentication at the token endpoint.
package oidctest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"time"
)

const KeyID = "test-key"

type Provider struct {
	Server       *httptest.Server
	ClientID     string
	ClientSecret string
	Key          *rsa.PrivateKey

	mu sync.Mutex
	// Claims are added to every issued ID token (sub defaults to "user-1").
	Claims map[string]any
	// TokenHook may rewrite the ID token claims just before signing.
	TokenHook func(map[string]any)
	codes     map[string]pendingCode
	Exchanges int
}

type pendingCode struct {
	redirectURI string
	challenge   string
	nonce       string
}

func New() *Provider {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	p := &Provider{
		ClientID:     "kervan",
		ClientSecret: "test-secret",
		Key:          key,
		Claims:       map[string]any{"sub": "user-1"},
		codes:        map[string]pendingCode{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("/jwks", p.jwks)
	mux.HandleFunc("/authorize", p.authorize)
	mux.HandleFunc("/token", p.token)
	p.Server = httptest.NewServer(mux)
	return p
}

func (p *Provider) Close() { p.Server.Close() }

func (p *Provider) Issuer() string { return p.Server.URL }

func (p *Provider) discovery(w http.ResponseWriter, _ *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer":                 p.Issuer(),
		"authorization_endpoint": p.Issuer() + "/authorize",
		"token_endpoint":         p.Issuer() + "/token",
		"jwks_uri":               p.Issuer() + "/jwks",
	})
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (p *Provider) jwks(w http.ResponseWriter, _ *http.Request) {
	pub := p.Key.PublicKey
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
		"kty": "RSA", "kid": KeyID, "use": "sig", "alg": "RS256",
		"n": b64(pub.N.Bytes()), "e": b64(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}

// authorize immediately "logs the user in" and redirects back with a code.
func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("client_id") != p.ClientID || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" {
		http.Error(w, "bad authorization request", http.StatusBadRequest)
		return
	}
	codeBytes := make([]byte, 16)
	_, _ = rand.Read(codeBytes)
	code := b64(codeBytes)
	p.mu.Lock()
	p.codes[code] = pendingCode{redirectURI: q.Get("redirect_uri"), challenge: q.Get("code_challenge"), nonce: q.Get("nonce")}
	p.mu.Unlock()
	target, _ := url.Parse(q.Get("redirect_uri"))
	back := target.Query()
	back.Set("code", code)
	back.Set("state", q.Get("state"))
	target.RawQuery = back.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	user, pass, ok := r.BasicAuth()
	clientID, _ := url.QueryUnescape(user)
	secret, _ := url.QueryUnescape(pass)
	if !ok || clientID != p.ClientID || secret != p.ClientSecret {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client"})
		return
	}
	_ = r.ParseForm()
	code := r.PostForm.Get("code")
	p.mu.Lock()
	pending, found := p.codes[code]
	delete(p.codes, code) // codes are single use
	p.Exchanges++
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !found || pending.redirectURI != r.PostForm.Get("redirect_uri") || b64(sum[:]) != pending.challenge {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
		return
	}
	now := time.Now()
	claims := map[string]any{
		"iss": p.Issuer(), "aud": p.ClientID, "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
		"nonce": pending.nonce,
	}
	p.mu.Lock()
	for k, v := range p.Claims {
		claims[k] = v
	}
	hook := p.TokenHook
	p.mu.Unlock()
	if hook != nil {
		hook(claims)
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"id_token": p.Sign(claims), "access_token": "at", "token_type": "Bearer"})
}

// Sign returns an RS256 JWT over claims with the provider key.
func (p *Provider) Sign(claims map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": KeyID, "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	input := b64(header) + "." + b64(payload)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.Key, crypto.SHA256, digest[:])
	if err != nil {
		panic(err)
	}
	return input + "." + b64(sig)
}

// SetClaims replaces the extra claims for subsequent tokens.
func (p *Provider) SetClaims(claims map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Claims = claims
}
