package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neikow/shuttle/internal/config"
)

func authConfig(t *testing.T, srv *HTTPServer) map[string]any {
	t.Helper()
	ts := httptest.NewServer(srv)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/auth/config")
	if err != nil {
		t.Fatalf("GET /auth/config: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestAuthConfig_DisabledByDefault(t *testing.T) {
	srv := NewHTTPServer("tok", nil, nil)
	out := authConfig(t, srv)
	if out["oidc_enabled"] != false {
		t.Fatalf("oidc_enabled = %v, want false", out["oidc_enabled"])
	}
	if _, ok := out["issuer"]; ok {
		t.Fatalf("issuer should be omitted when disabled: %v", out)
	}
}

func TestAuthConfig_AdvertisesOIDC(t *testing.T) {
	m := newMockOIDC(t)
	srv := NewHTTPServer("tok", nil, nil)
	auth, err := NewOIDCAuthenticator(context.Background(), config.OIDCConfig{
		Issuer:      m.issuer,
		Audience:    oidcAudience,
		RoleMapping: map[string]string{"sh-admins": "admin"},
		Scopes:      "openid profile groups",
	})
	if err != nil {
		t.Fatalf("NewOIDCAuthenticator: %v", err)
	}
	srv.SetOIDC(auth)

	out := authConfig(t, srv)
	if out["oidc_enabled"] != true {
		t.Fatalf("oidc_enabled = %v, want true", out["oidc_enabled"])
	}
	if out["issuer"] != m.issuer {
		t.Fatalf("issuer = %v, want %s", out["issuer"], m.issuer)
	}
	if out["client_id"] != oidcAudience {
		t.Fatalf("client_id = %v, want %s", out["client_id"], oidcAudience)
	}
	if out["scopes"] != "openid profile groups" {
		t.Fatalf("scopes = %v", out["scopes"])
	}
}

func TestAuthConfig_ScopesDefault(t *testing.T) {
	m := newMockOIDC(t)
	auth, err := NewOIDCAuthenticator(context.Background(), config.OIDCConfig{
		Issuer:      m.issuer,
		Audience:    oidcAudience,
		RoleMapping: map[string]string{"sh-admins": "admin"},
	})
	if err != nil {
		t.Fatalf("NewOIDCAuthenticator: %v", err)
	}
	if got := auth.PublicConfig().Scopes; got != "openid profile email" {
		t.Fatalf("default scopes = %q", got)
	}
}

func TestCSPForUI_AllowsIssuerWhenOIDCEnabled(t *testing.T) {
	// Without OIDC, the UI CSP is the strict same-origin default.
	base := NewHTTPServer("tok", nil, nil)
	if got := base.cspForUI(); got != uiCSP {
		t.Fatalf("default CSP changed: %q", got)
	}
	if strings.Contains(base.cspForUI(), "connect-src 'self' http") {
		t.Fatal("default CSP must not allow a cross-origin connect-src")
	}

	// With OIDC, the issuer origin is added to connect-src so the SPA can run
	// discovery + the PKCE token exchange against the IdP.
	m := newMockOIDC(t)
	srv := NewHTTPServer("tok", nil, nil)
	auth, err := NewOIDCAuthenticator(context.Background(), config.OIDCConfig{
		Issuer:      m.issuer,
		Audience:    oidcAudience,
		RoleMapping: map[string]string{"sh-admins": "admin"},
	})
	if err != nil {
		t.Fatalf("NewOIDCAuthenticator: %v", err)
	}
	srv.SetOIDC(auth)

	csp := srv.cspForUI()
	if !strings.Contains(csp, "connect-src 'self' "+m.issuer) {
		t.Fatalf("issuer origin not in connect-src: %q", csp)
	}
	// The rest of the policy is unchanged (still frames-denied, scripts self).
	if !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "script-src 'self'") {
		t.Fatalf("CSP lost baseline directives: %q", csp)
	}
}
