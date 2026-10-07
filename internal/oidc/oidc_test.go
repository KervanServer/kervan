package oidc

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/oidc/oidctest"
)

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// keyServer serves discovery plus an arbitrary JWKS document.
func keyServer(t *testing.T, keys []map[string]any) (*httptest.Server, *Provider) {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer": srv.URL, "authorization_endpoint": srv.URL + "/a", "token_endpoint": srv.URL + "/t", "jwks_uri": srv.URL + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p, err := NewProvider(Config{Issuer: srv.URL, ClientID: "kervan", RedirectURL: "http://rp/cb"})
	if err != nil {
		t.Fatal(err)
	}
	return srv, p
}

func baseClaims(issuer string) map[string]any {
	now := time.Now()
	return map[string]any{"iss": issuer, "aud": "kervan", "sub": "u1", "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "nonce": "n1"}
}

func signed(alg, kid string, claims map[string]any, sign func(input []byte) []byte) string {
	h, _ := json.Marshal(map[string]string{"alg": alg, "kid": kid})
	c, _ := json.Marshal(claims)
	input := b64(h) + "." + b64(c)
	return input + "." + b64(sign([]byte(input)))
}

func ecSign(key *ecdsa.PrivateKey, h crypto.Hash) func([]byte) []byte {
	return func(input []byte) []byte {
		hasher := h.New()
		hasher.Write(input)
		r, s, err := ecdsa.Sign(rand.Reader, key, hasher.Sum(nil))
		if err != nil {
			panic(err)
		}
		size := (key.Curve.Params().BitSize + 7) / 8
		out := make([]byte, 2*size)
		r.FillBytes(out[:size])
		s.FillBytes(out[size:])
		return out
	}
}

func TestVerifyIDTokenAlgorithms(t *testing.T) {
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	ec256, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ec384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	edPub, edPriv, _ := ed25519.GenerateKey(rand.Reader)
	keys := []map[string]any{
		{"kty": "RSA", "kid": "rsa", "n": b64(rsaKey.N.Bytes()), "e": b64(big.NewInt(int64(rsaKey.E)).Bytes())},
		{"kty": "EC", "kid": "ec256", "crv": "P-256", "x": b64(ec256.X.Bytes()), "y": b64(ec256.Y.Bytes())},
		{"kty": "EC", "kid": "ec384", "crv": "P-384", "x": b64(ec384.X.Bytes()), "y": b64(ec384.Y.Bytes())},
		{"kty": "OKP", "kid": "ed", "crv": "Ed25519", "x": b64(edPub)},
		{"kty": "RSA", "kid": "enc", "use": "enc", "n": b64(rsaKey.N.Bytes()), "e": "AQAB"},
	}
	srv, p := keyServer(t, keys)
	ctx := context.Background()

	rsSign := func(h crypto.Hash, pss bool) func([]byte) []byte {
		return func(input []byte) []byte {
			hasher := h.New()
			hasher.Write(input)
			var sig []byte
			var err error
			if pss {
				sig, err = rsa.SignPSS(rand.Reader, rsaKey, h, hasher.Sum(nil), &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
			} else {
				sig, err = rsa.SignPKCS1v15(rand.Reader, rsaKey, h, hasher.Sum(nil))
			}
			if err != nil {
				panic(err)
			}
			return sig
		}
	}
	good := map[string]string{
		"RS256":  signed("RS256", "rsa", baseClaims(srv.URL), rsSign(crypto.SHA256, false)),
		"RS512":  signed("RS512", "rsa", baseClaims(srv.URL), rsSign(crypto.SHA512, false)),
		"PS256":  signed("PS256", "rsa", baseClaims(srv.URL), rsSign(crypto.SHA256, true)),
		"ES256":  signed("ES256", "ec256", baseClaims(srv.URL), ecSign(ec256, crypto.SHA256)),
		"ES384":  signed("ES384", "ec384", baseClaims(srv.URL), ecSign(ec384, crypto.SHA384)),
		"EdDSA":  signed("EdDSA", "ed", baseClaims(srv.URL), func(in []byte) []byte { return ed25519.Sign(edPriv, in) }),
		"no-kid": signed("RS256", "", baseClaims(srv.URL), rsSign(crypto.SHA256, false)),
	}
	for name, tok := range good {
		claims, err := p.VerifyIDToken(ctx, tok, "n1")
		if err != nil || claims.String("sub") != "u1" {
			t.Errorf("%s: %v", name, err)
		}
	}

	hmacSign := func(in []byte) []byte {
		m := hmac.New(sha256.New, []byte("public-key-as-secret"))
		m.Write(in)
		return m.Sum(nil)
	}
	tamper := func(tok string) string { return tok[:len(tok)-4] + "AAAA" }
	bad := map[string]string{
		"alg none":         signed("none", "rsa", baseClaims(srv.URL), func([]byte) []byte { return nil }),
		"alg HS256":        signed("HS256", "rsa", baseClaims(srv.URL), hmacSign),
		"tampered":         tamper(good["RS256"]),
		"ES256 on P-384":   signed("ES256", "ec384", baseClaims(srv.URL), ecSign(ec384, crypto.SHA256)),
		"enc-only key":     signed("RS256", "enc", baseClaims(srv.URL), rsSign(crypto.SHA256, false)),
		"malformed":        "abc.def",
		"wrong signer key": signed("ES256", "ec256", baseClaims(srv.URL), ecSign(func() *ecdsa.PrivateKey { k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader); return k }(), crypto.SHA256)),
	}
	for name, tok := range bad {
		if _, err := p.VerifyIDToken(ctx, tok, "n1"); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestVerifyIDTokenClaims(t *testing.T) {
	idp := oidctest.New()
	defer idp.Close()
	p, err := NewProvider(Config{Issuer: idp.Issuer(), ClientID: idp.ClientID, RedirectURL: "http://rp/cb"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mk := func(edit func(map[string]any)) string {
		c := baseClaims(idp.Issuer())
		c["aud"] = idp.ClientID
		edit(c)
		return idp.Sign(c)
	}
	if _, err := p.VerifyIDToken(ctx, mk(func(map[string]any) {}), "n1"); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if _, err := p.VerifyIDToken(ctx, mk(func(c map[string]any) { c["aud"] = []any{"other", idp.ClientID}; c["azp"] = idp.ClientID }), "n1"); err != nil {
		t.Fatalf("multi-audience token with azp rejected: %v", err)
	}
	cases := map[string]func(map[string]any){
		"wrong issuer":          func(c map[string]any) { c["iss"] = "https://evil.example" },
		"wrong audience":        func(c map[string]any) { c["aud"] = "someone-else" },
		"multi-aud without azp": func(c map[string]any) { c["aud"] = []any{"other", idp.ClientID} },
		"expired":               func(c map[string]any) { c["exp"] = time.Now().Add(-2 * time.Minute).Unix() },
		"missing exp":           func(c map[string]any) { delete(c, "exp") },
		"issued in future":      func(c map[string]any) { c["iat"] = time.Now().Add(5 * time.Minute).Unix() },
		"not yet valid":         func(c map[string]any) { c["nbf"] = time.Now().Add(5 * time.Minute).Unix() },
		"nonce mismatch":        func(c map[string]any) { c["nonce"] = "other" },
		"missing sub":           func(c map[string]any) { delete(c, "sub") },
	}
	for name, edit := range cases {
		if _, err := p.VerifyIDToken(ctx, mk(edit), "n1"); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestOffCurveECKeyIgnored(t *testing.T) {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	badY := new(big.Int).Add(k.Y, big.NewInt(1))
	srv, p := keyServer(t, []map[string]any{{"kty": "EC", "kid": "bad", "crv": "P-256", "x": b64(k.X.Bytes()), "y": b64(badY.Bytes())}})
	tok := signed("ES256", "bad", baseClaims(srv.URL), ecSign(k, crypto.SHA256))
	if _, err := p.VerifyIDToken(context.Background(), tok, "n1"); err == nil {
		t.Fatal("token verified with an off-curve key")
	}
}

func TestWeakRSAKeyIgnored(t *testing.T) {
	weak, _ := rsa.GenerateKey(rand.Reader, 1024)
	srv, p := keyServer(t, []map[string]any{{"kty": "RSA", "kid": "weak", "n": b64(weak.N.Bytes()), "e": "AQAB"}})
	tok := signed("RS256", "weak", baseClaims(srv.URL), func(in []byte) []byte {
		d := sha256.Sum256(in)
		s, _ := rsa.SignPKCS1v15(rand.Reader, weak, crypto.SHA256, d[:])
		return s
	})
	if _, err := p.VerifyIDToken(context.Background(), tok, "n1"); err == nil {
		t.Fatal("token signed by a 1024-bit key accepted")
	}
}

func TestDiscoveryIssuerMismatch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": "https://other.example", "authorization_endpoint": "x", "token_endpoint": "y", "jwks_uri": "z"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p, _ := NewProvider(Config{Issuer: srv.URL, ClientID: "c", RedirectURL: "http://rp/cb"})
	if _, err := p.Metadata(context.Background()); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("issuer mismatch not detected: %v", err)
	}
}

// TestAuthCodeFlowWithPKCE drives the full browser round trip against the
// test provider, including single-use codes.
func TestAuthCodeFlowWithPKCE(t *testing.T) {
	idp := oidctest.New()
	defer idp.Close()
	idp.SetClaims(map[string]any{"sub": "abc", "preferred_username": "ann", "groups": []any{"eng"}})
	p, _ := NewProvider(Config{Issuer: idp.Issuer(), ClientID: idp.ClientID, ClientSecret: idp.ClientSecret, RedirectURL: "http://rp.example/cb", Scopes: []string{"profile", "groups"}})
	ctx := context.Background()

	req, err := NewAuthRequest()
	if err != nil {
		t.Fatal(err)
	}
	authURL, err := p.AuthCodeURL(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if u, _ := url.Parse(authURL); !strings.HasPrefix(u.Query().Get("scope"), "openid ") || u.Query().Get("code_challenge") == "" {
		t.Fatalf("auth URL missing openid scope or PKCE: %s", authURL)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	cb, _ := url.Parse(resp.Header.Get("Location"))
	if cb.Query().Get("state") != req.State {
		t.Fatal("state not echoed")
	}
	code := cb.Query().Get("code")

	// A wrong PKCE verifier is refused by the provider.
	wrong := req
	wrong.Verifier = "not-the-verifier"
	if _, err := p.Exchange(ctx, code, wrong); err == nil {
		t.Fatal("exchange with wrong verifier succeeded")
	}
	// The failed attempt burned the code; start a fresh login.
	resp, _ = client.Get(authURL)
	resp.Body.Close()
	cb, _ = url.Parse(resp.Header.Get("Location"))
	code = cb.Query().Get("code")

	claims, err := p.Exchange(ctx, code, req)
	if err != nil {
		t.Fatal(err)
	}
	if claims.String("preferred_username") != "ann" || claims.Strings("groups")[0] != "eng" {
		t.Fatalf("claims = %v", claims)
	}
	if _, err := p.Exchange(ctx, code, req); err == nil {
		t.Fatal("authorization code reused")
	}
}
