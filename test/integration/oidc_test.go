//go:build integration

package integration

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// TestOIDCEnforcesRolesAndAuditsSubject drives a real orchestrator subprocess
// configured with OIDC against an in-process mock issuer, proving end-to-end:
// (1) a validly-signed JWT mapped to admin may mint a control token while a
// read-scoped JWT is forbidden (403) and an absent/garbage token is 401; and
// (2) the OIDC subject becomes the audit actor.
func TestOIDCEnforcesRolesAndAuditsSubject(t *testing.T) {
	root := repoRoot(t)
	bin := buildBinary(t, root)

	// The issuer must be reachable before the orchestrator starts (OIDC
	// discovery happens at boot), so stand it up first.
	oidc := newMockOIDCServer(t)

	grpcPort := freePort(t)
	httpPort := freePort(t)
	base := fmt.Sprintf("http://127.0.0.1:%d", httpPort)

	cfg := filepath.Join(t.TempDir(), "config.yml")
	writeFileOrFail(t, cfg, fmt.Sprintf(`bearer_token: static-admin
grpc_addr: "127.0.0.1:%d"
http_addr: "127.0.0.1:%d"
data_dir: %s
oidc:
  issuer: %q
  audience: shuttle
  roles_claim: groups
  username_claim: email
  role_mapping:
    sh-admins: admin
    sh-viewers: read
`, grpcPort, httpPort, t.TempDir(), oidc.issuer))

	ctx := t.Context()
	startProc(ctx, t, "orchestrator", bin, "orchestrator", "--config", cfg)
	waitFor(t, 30*time.Second, "orchestrator /healthz", func() bool {
		code, _ := httpDo(t, http.MethodGet, base+"/healthz", "")
		return code == http.StatusOK
	})

	adminJWT := oidc.mint(t, map[string]any{"sub": "u-admin", "email": "boss@corp.example", "groups": []string{"sh-admins"}})
	readJWT := oidc.mint(t, map[string]any{"sub": "u-read", "email": "viewer@corp.example", "groups": []string{"sh-viewers"}})

	// read-scoped JWT may list deploys...
	if c, b := httpDo(t, http.MethodGet, base+"/deploys", readJWT); c != http.StatusOK {
		t.Fatalf("read JWT GET /deploys: want 200, got %d: %s", c, b)
	}
	// ...but not mint tokens (admin tier) → 403.
	if c, b := httpPostJSON(t, base+"/tokens", readJWT, `{"name":"x","role":"read"}`); c != http.StatusForbidden {
		t.Fatalf("read JWT POST /tokens: want 403, got %d: %s", c, b)
	}
	// No token → 401; garbage JWT-shaped token → 401.
	if c, _ := httpDo(t, http.MethodGet, base+"/deploys", ""); c != http.StatusUnauthorized {
		t.Fatalf("no token GET /deploys: want 401, got %d", c)
	}
	if c, _ := httpDo(t, http.MethodGet, base+"/deploys", "aaa.bbb.ccc"); c != http.StatusUnauthorized {
		t.Fatalf("garbage JWT GET /deploys: want 401, got %d", c)
	}

	// admin JWT mints a control token (an audited admin action).
	if c, b := httpPostJSON(t, base+"/tokens", adminJWT, `{"name":"minted-by-oidc","role":"read"}`); c != http.StatusCreated {
		t.Fatalf("admin JWT POST /tokens: want 201, got %d: %s", c, b)
	}

	// The audit log attributes token.create to the OIDC subject (email claim).
	code, body := httpDo(t, http.MethodGet, base+"/audit?action=token.create", adminJWT)
	if code != http.StatusOK {
		t.Fatalf("GET /audit: want 200, got %d: %s", code, body)
	}
	var entries []struct {
		Actor  string `json:"actor"`
		Action string `json:"action"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &entries); err != nil {
		t.Fatalf("decode audit: %v (%s)", err, body)
	}
	if len(entries) == 0 {
		t.Fatalf("no token.create audit entry: %s", body)
	}
	if got := entries[0].Actor; got != "boss@corp.example" {
		t.Fatalf("audit actor = %q, want boss@corp.example (OIDC subject)", got)
	}
}

// mockOIDCServer is an in-process OIDC issuer: discovery + JWKS + a JWT minter.
type mockOIDCServer struct {
	issuer string
	signer jose.Signer
}

func newMockOIDCServer(t *testing.T) *mockOIDCServer {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}
	const kid = "it-key"
	jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key: priv.Public(), KeyID: kid, Algorithm: "RS256", Use: "sig",
	}}}

	m := &mockOIDCServer{}
	mux := http.NewServeMux()
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
	// Bind to all interfaces on a loopback-reachable address so the orchestrator
	// subprocess can reach it.
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

func (m *mockOIDCServer) mint(t *testing.T, claims map[string]any) string {
	t.Helper()
	c := map[string]any{
		"iss": m.issuer,
		"aud": "shuttle",
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
