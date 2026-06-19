package orchestrator

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/neikow/shuttle/internal/config"
)

const oidcAudience = "shuttle"

// mockOIDC is an in-process OpenID Connect issuer for tests: it serves a
// discovery document and a JWKS, and mints JWTs signed by its key.
type mockOIDC struct {
	issuer string
	signer jose.Signer
}

// newMockOIDC stands up a discovery + JWKS endpoint backed by a fresh RSA key
// and returns a minter for signed tokens. Torn down on test cleanup.
func newMockOIDC(t *testing.T) *mockOIDC {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}
	const kid = "test-key"
	jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key: priv.Public(), KeyID: kid, Algorithm: "RS256", Use: "sig",
	}}}

	mux := http.NewServeMux()
	m := &mockOIDC{}
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                m.issuer,
			"jwks_uri":                              m.issuer + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	m.issuer = srv.URL

	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: priv, KeyID: kid}},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	m.signer = signer
	return m
}

// mint signs a JWT with the given claims, defaulting iss/aud/exp/iat so callers
// only set what a case actually varies.
func (m *mockOIDC) mint(t *testing.T, claims map[string]any) string {
	t.Helper()
	c := map[string]any{
		"iss": m.issuer,
		"aud": oidcAudience,
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}
	maps.Copy(c, claims)
	payload, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	obj, err := m.signer.Sign(payload)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	tok, err := obj.CompactSerialize()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	return tok
}

// oidcTestServer wires an HTTPServer to a mock OIDC issuer with a standard
// group→role mapping and three role-gated echo routes.
func oidcTestServer(t *testing.T, m *mockOIDC) *HTTPServer {
	t.Helper()
	srv := newHTTPTestServer(t)
	auth, err := NewOIDCAuthenticator(context.Background(), config.OIDCConfig{
		Issuer:        m.issuer,
		Audience:      oidcAudience,
		RolesClaim:    "groups",
		UsernameClaim: "email",
		RoleMapping: map[string]string{
			"sh-admins":  "admin",
			"sh-deploy":  "deploy",
			"sh-viewers": "read",
		},
	})
	if err != nil {
		t.Fatalf("NewOIDCAuthenticator: %v", err)
	}
	srv.SetOIDC(auth)
	srv.mux.HandleFunc("GET /t/read", srv.requireRole(RoleRead, echoActor))
	srv.mux.HandleFunc("GET /t/deploy", srv.requireRole(RoleDeploy, echoActor))
	srv.mux.HandleFunc("GET /t/admin", srv.requireRole(RoleAdmin, echoActor))
	return srv
}

func TestOIDC_RoleMappingAndEnforcement(t *testing.T) {
	m := newMockOIDC(t)
	srv := oidcTestServer(t, m)

	adminTok := m.mint(t, map[string]any{"sub": "u1", "email": "boss@x.io", "groups": []string{"sh-admins"}})
	readTok := m.mint(t, map[string]any{"sub": "u2", "email": "viewer@x.io", "groups": []string{"sh-viewers"}})
	// Two groups: highest-ranked (deploy) wins over read.
	multiTok := m.mint(t, map[string]any{"sub": "u3", "email": "dev@x.io", "groups": []string{"sh-viewers", "sh-deploy"}})
	// Valid token, but no group maps to a role → authenticated yet unauthorized.
	noRoleTok := m.mint(t, map[string]any{"sub": "u4", "email": "stranger@x.io", "groups": []string{"other"}})

	cases := []struct {
		name, path, tok string
		code            int
		actor           string
	}{
		{"admin→admin", "/t/admin", adminTok, 200, "boss@x.io"},
		{"read→read", "/t/read", readTok, 200, "viewer@x.io"},
		{"read→deploy forbidden", "/t/deploy", readTok, http.StatusForbidden, ""},
		{"multi(deploy)→deploy", "/t/deploy", multiTok, 200, "dev@x.io"},
		{"multi(deploy)→admin forbidden", "/t/admin", multiTok, http.StatusForbidden, ""},
		{"no-role→read forbidden(403 not 401)", "/t/read", noRoleTok, http.StatusForbidden, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, bearerReq(http.MethodGet, c.path, c.tok))
			if w.Code != c.code {
				t.Fatalf("code = %d, want %d (%s)", w.Code, c.code, w.Body.String())
			}
			if c.code == 200 && w.Body.String() != c.actor {
				t.Errorf("actor = %q, want %q", w.Body.String(), c.actor)
			}
		})
	}
}

func TestOIDC_RejectsBadTokens(t *testing.T) {
	m := newMockOIDC(t)
	srv := oidcTestServer(t, m)

	// Wrong audience.
	wrongAud := m.mint(t, map[string]any{"sub": "u", "aud": "someone-else", "groups": []string{"sh-admins"}})
	// Expired.
	expired := m.mint(t, map[string]any{"sub": "u", "exp": time.Now().Add(-time.Hour).Unix(), "groups": []string{"sh-admins"}})

	for _, c := range []struct {
		name, tok string
	}{
		{"wrong audience", wrongAud},
		{"expired", expired},
		{"garbage non-JWT", "not-a-jwt"},
		{"jwt-shaped garbage", "aaa.bbb.ccc"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, bearerReq(http.MethodGet, "/t/read", c.tok))
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("code = %d, want 401 (%s)", w.Code, w.Body.String())
			}
		})
	}
}

// TestOIDC_UsernameFallsBackToSub proves an absent username claim falls back to
// the subject, so the audit actor is never empty for a valid token.
func TestOIDC_UsernameFallsBackToSub(t *testing.T) {
	m := newMockOIDC(t)
	srv := oidcTestServer(t, m)
	tok := m.mint(t, map[string]any{"sub": "subject-123", "groups": []string{"sh-admins"}})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, bearerReq(http.MethodGet, "/t/admin", tok))
	if w.Code != 200 {
		t.Fatalf("code = %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if w.Body.String() != "subject-123" {
		t.Errorf("actor = %q, want subject-123", w.Body.String())
	}
}

// TestOIDC_StaticBearerStillAdmin proves the bootstrap bearer and control tokens
// keep working when OIDC is enabled (OIDC is additive, not a replacement).
func TestOIDC_StaticBearerStillAdmin(t *testing.T) {
	m := newMockOIDC(t)
	srv := oidcTestServer(t, m)

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, bearerReq(http.MethodGet, "/t/admin", testToken))
	if w.Code != 200 || w.Body.String() != "operator" {
		t.Fatalf("static bearer: code=%d body=%q, want 200/operator", w.Code, w.Body.String())
	}

	deployTok := mintToken(t, srv, "id-d", "deployer", "deploy")
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, bearerReq(http.MethodGet, "/t/deploy", deployTok))
	if w.Code != 200 || w.Body.String() != "deployer" {
		t.Fatalf("control token: code=%d body=%q, want 200/deployer", w.Code, w.Body.String())
	}
}

func TestLooksLikeJWT(t *testing.T) {
	for tok, want := range map[string]bool{
		"a.b.c":       true,
		"a.b":         false,
		"a.b.c.d":     false,
		"":            false,
		".b.c":        false,
		"a..c":        false,
		"opaque-token": false,
	} {
		if got := looksLikeJWT(tok); got != want {
			t.Errorf("looksLikeJWT(%q) = %v, want %v", tok, got, want)
		}
	}
}
