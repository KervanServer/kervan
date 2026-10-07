package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/config"
	icrypto "github.com/kervanserver/kervan/internal/crypto"
	"github.com/kervanserver/kervan/internal/oidc/oidctest"
)

type oidcEnv struct {
	app  *App
	idp  *oidctest.Provider
	base string
}

func startOIDCApp(t *testing.T, editCfg func(*config.OIDCConfig), edit func(*oidcEnv)) *oidcEnv {
	t.Helper()
	idp := oidctest.New()
	t.Cleanup(idp.Close)
	dir := t.TempDir()
	cfg := crossProtoConfig(dir)
	cfg.SFTP.HostKeyDir = filepath.Join(dir, "host_keys")
	if _, err := icrypto.EnsureHostKeys(cfg.SFTP.HostKeyDir); err != nil {
		t.Fatal(err)
	}
	base := "http://" + crossProtoAddr(cfg.WebUI.Port)
	o := &cfg.WebUI.OIDC
	o.Enabled = true
	o.Issuer = idp.Issuer()
	o.ClientID = idp.ClientID
	o.ClientSecret = idp.ClientSecret
	o.RedirectURL = base + "/api/v1/auth/oidc/callback"
	o.Scopes = []string{"openid", "profile", "groups"}
	o.AdminGroups = []string{"kervan-admins"}
	o.GroupMapping = map[string]string{"Engineering": "eng"}
	if editCfg != nil {
		editCfg(o)
	}

	app, err := New(cfg, filepath.Join(dir, "kervan.yaml"), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	env := &oidcEnv{app: app, idp: idp, base: base}
	if edit != nil {
		edit(env)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := app.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = app.Close() })
	return env
}

// signIn runs the browser round trip and returns the final redirect query
// (oidc_code or oidc_error).
func (e *oidcEnv) signIn(t *testing.T, client *http.Client) url.Values {
	t.Helper()
	if client == nil {
		jar, _ := cookiejar.New(nil)
		client = &http.Client{Jar: jar}
	}
	client.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
		if req.URL.Path == "/" {
			return http.ErrUseLastResponse
		}
		return nil
	}
	resp, err := client.Get(e.base + "/api/v1/auth/oidc/login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || loc.Path != "/" {
		t.Fatalf("sign-in did not end on the SPA: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	return loc.Query()
}

func (e *oidcEnv) exchange(t *testing.T, code string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"code": code})
	resp, err := http.Post(e.base+"/api/v1/auth/oidc/exchange", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestOIDCSignInProvisionsAndMapsGroups(t *testing.T) {
	env := startOIDCApp(t, nil, func(e *oidcEnv) {
		if err := e.app.groups.Create(&auth.Group{Name: "eng", Permissions: auth.DefaultUserPermissions()}); err != nil {
			t.Fatal(err)
		}
	})

	// The login screen discovers SSO.
	resp, err := http.Get(env.base + "/api/v1/auth/methods")
	if err != nil {
		t.Fatal(err)
	}
	var methods map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&methods)
	resp.Body.Close()
	if methods["oidc"].(map[string]any)["enabled"] != true {
		t.Fatalf("methods = %v", methods)
	}

	env.idp.SetClaims(map[string]any{
		"sub": "sub-ann", "preferred_username": "ann", "email": "ann@example.com", "email_verified": true,
		"groups": []any{"Engineering", "kervan-admins", "unrelated"},
	})
	q := env.signIn(t, nil)
	if q.Get("oidc_error") != "" {
		t.Fatalf("sign-in error: %s", q.Get("oidc_error"))
	}
	code, out := env.exchange(t, q.Get("oidc_code"))
	if code != http.StatusOK || out["token"] == "" {
		t.Fatalf("exchange: %d %v", code, out)
	}
	if again, _ := env.exchange(t, q.Get("oidc_code")); again != http.StatusBadRequest {
		t.Fatalf("one-time code reused: %d", again)
	}

	// The session token works against the API, as an admin.
	req, _ := http.NewRequest(http.MethodGet, env.base+"/api/v1/groups", nil)
	req.Header.Set("Authorization", "Bearer "+out["token"].(string))
	apiResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	apiResp.Body.Close()
	if apiResp.StatusCode != http.StatusOK {
		t.Fatalf("admin API call with OIDC session: %d", apiResp.StatusCode)
	}

	ann, _ := env.app.authRepo.GetByUsername("ann")
	if ann.AuthProvider != auth.AuthProviderOIDC || ann.Type != auth.UserTypeAdmin || ann.PrimaryGroup != "eng" || ann.HomeDir != "/ann" || ann.Email != "ann@example.com" {
		t.Fatalf("provisioned user = %+v", ann)
	}

	// Leaving the admin group demotes on the next sign-in.
	env.idp.SetClaims(map[string]any{"sub": "sub-ann", "preferred_username": "ann", "groups": []any{"Engineering"}})
	if q := env.signIn(t, nil); q.Get("oidc_code") == "" {
		t.Fatalf("second sign-in failed: %v", q)
	}
	if ann, _ = env.app.authRepo.GetByUsername("ann"); ann.Type != auth.UserTypeVirtual {
		t.Fatal("admin role not revoked")
	}
}

func TestOIDCSignInRejections(t *testing.T) {
	env := startOIDCApp(t, nil, func(e *oidcEnv) {
		if _, err := e.app.auth.CreateUser("root", "StrongPass123!", "/", true); err != nil {
			t.Fatal(err)
		}
	})

	// A provider asserting a local admin's username cannot take it over.
	env.idp.SetClaims(map[string]any{"sub": "evil", "preferred_username": "root"})
	if q := env.signIn(t, nil); q.Get("oidc_error") != "account_conflict" {
		t.Fatalf("takeover result: %v", q)
	}

	// A callback without the browser's state cookie (CSRF / login fixation)
	// is refused before the code is redeemed.
	before := env.idp.Exchanges
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noRedirect.Get(env.base + "/api/v1/auth/oidc/callback?code=x&state=forged")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/?oidc_error=invalid_state" {
		t.Fatalf("forged callback -> %q", loc)
	}
	if env.idp.Exchanges != before {
		t.Fatal("forged callback reached the token endpoint")
	}

	// Provider errors are passed through as sanitized codes.
	resp, _ = noRedirect.Get(env.base + "/api/v1/auth/oidc/callback?error=access_denied")
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/?oidc_error=access_denied" {
		t.Fatalf("provider error -> %q", loc)
	}
	resp, _ = noRedirect.Get(env.base + "/api/v1/auth/oidc/callback?error=%3Cscript%3E")
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/?oidc_error=provider_error" {
		t.Fatalf("unsanitized provider error -> %q", loc)
	}

	// Missing username claim.
	env.idp.SetClaims(map[string]any{"sub": "s2"})
	if q := env.signIn(t, nil); q.Get("oidc_error") != "missing_username" {
		t.Fatalf("missing username: %v", q)
	}
}

func TestOIDCAllowedGroupsAndAutoCreate(t *testing.T) {
	env := startOIDCApp(t, func(o *config.OIDCConfig) {
		o.AllowedGroups = []string{"kervan-users"}
		o.AutoCreate = false
	}, nil)

	env.idp.SetClaims(map[string]any{"sub": "s1", "preferred_username": "zed", "groups": []any{"other"}})
	if q := env.signIn(t, nil); q.Get("oidc_error") != "not_allowed" {
		t.Fatalf("allowed_groups: %v", q)
	}
	env.idp.SetClaims(map[string]any{"sub": "s1", "preferred_username": "zed", "groups": []any{"kervan-users"}})
	if q := env.signIn(t, nil); q.Get("oidc_error") != "not_provisioned" {
		t.Fatalf("auto_create=false: %v", q)
	}
	if u, _ := env.app.authRepo.GetByUsername("zed"); u != nil {
		t.Fatal("account created with auto_create=false")
	}
}
