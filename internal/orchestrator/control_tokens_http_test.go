package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neikow/shuttle/internal/ledger"
)

// postJSON issues an authed POST with a JSON body.
func postJSON(method, path, tok, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestControlTokenCRUD(t *testing.T) {
	srv := newHTTPTestServer(t)

	// Create a deploy token via the static admin bearer.
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, postJSON(http.MethodPost, "/tokens", testToken, `{"name":"ci-bot","role":"deploy"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", w.Code, w.Body.String())
	}
	var created struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Role  string `json:"role"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Token == "" || created.ID == "" || created.Role != "deploy" {
		t.Fatalf("unexpected create response: %+v", created)
	}

	// The minted token resolves to its role.
	role, name, ok := srv.resolveRole(context.Background(), created.Token)
	if !ok || role != RoleDeploy || name != "ci-bot" {
		t.Fatalf("resolveRole = (%v, %q, %v), want (deploy, ci-bot, true)", role, name, ok)
	}

	// token.create was audited.
	entries, err := srv.ledger.ListAudit(context.Background(), auditTokenCreate, 10)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(entries) != 1 || entries[0].Target != "ci-bot" || entries[0].Result != auditSuccess {
		t.Fatalf("token.create audit entry wrong: %+v", entries)
	}

	// List shows it (active, no hash field).
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, bearerReq(http.MethodGet, "/tokens", testToken))
	if w.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", w.Code)
	}
	var tokens []ledger.ControlToken
	if err := json.NewDecoder(w.Body).Decode(&tokens); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(tokens) != 1 || tokens[0].RevokedAt != nil {
		t.Fatalf("list = %+v, want 1 active token", tokens)
	}

	// Revoke it.
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, bearerReq(http.MethodDelete, "/tokens/"+created.ID, testToken))
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoke: want 204, got %d", w.Code)
	}
	if _, _, ok := srv.resolveRole(context.Background(), created.Token); ok {
		t.Fatal("revoked token still resolves")
	}

	// Revoking an unknown id → 404.
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, bearerReq(http.MethodDelete, "/tokens/nope", testToken))
	if w.Code != http.StatusNotFound {
		t.Fatalf("revoke unknown: want 404, got %d", w.Code)
	}
}

func TestControlTokenCreate_badRole(t *testing.T) {
	srv := newHTTPTestServer(t)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, postJSON(http.MethodPost, "/tokens", testToken, `{"name":"x","role":"god"}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad role: want 400, got %d", w.Code)
	}
}

// TestControlTokenCreate_requiresAdmin proves a non-admin token can't mint
// tokens: a deploy-role token gets 403 on POST /tokens.
func TestControlTokenCreate_requiresAdmin(t *testing.T) {
	srv := newHTTPTestServer(t)
	deployTok := mintToken(t, srv, "id-d", "deployer", "deploy")

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, postJSON(http.MethodPost, "/tokens", deployTok, `{"name":"x","role":"read"}`))
	if w.Code != http.StatusForbidden {
		t.Fatalf("deploy token minting: want 403, got %d", w.Code)
	}
}
