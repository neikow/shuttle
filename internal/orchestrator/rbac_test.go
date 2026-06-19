package orchestrator

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/neikow/shuttle/internal/token"
)

func TestParseRoleAndRank(t *testing.T) {
	for _, s := range []string{"read", "deploy", "admin"} {
		if _, err := ParseRole(s); err != nil {
			t.Errorf("ParseRole(%q) errored: %v", s, err)
		}
	}
	if _, err := ParseRole("superuser"); err == nil {
		t.Error("ParseRole(superuser) should error")
	}
	if roleRank(RoleRead) >= roleRank(RoleDeploy) || roleRank(RoleDeploy) >= roleRank(RoleAdmin) {
		t.Error("role ranks not strictly increasing read < deploy < admin")
	}
	if roleRank(Role("bogus")) != 0 {
		t.Error("unknown role must rank 0")
	}
}

// echoActor writes the resolved audit actor so tests can assert identity.
func echoActor(w http.ResponseWriter, r *http.Request) {
	_, _ = io.WriteString(w, auditActor(r))
}

func bearerReq(method, path, tok string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return req
}

// mintToken stores a control token and returns its plaintext.
func mintToken(t *testing.T, srv *HTTPServer, id, name, role string) string {
	t.Helper()
	tok, err := token.Generate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if err := srv.ledger.CreateControlToken(context.Background(), id, name, token.Hash(tok), role); err != nil {
		t.Fatalf("CreateControlToken: %v", err)
	}
	return tok
}

func TestRequireRole(t *testing.T) {
	srv := newHTTPTestServer(t)
	srv.mux.HandleFunc("GET /t/read", srv.requireRole(RoleRead, echoActor))
	srv.mux.HandleFunc("GET /t/deploy", srv.requireRole(RoleDeploy, echoActor))
	srv.mux.HandleFunc("GET /t/admin", srv.requireRole(RoleAdmin, echoActor))

	readTok := mintToken(t, srv, "id-r", "reader", "read")
	deployTok := mintToken(t, srv, "id-d", "deployer", "deploy")
	adminTok := mintToken(t, srv, "id-a", "boss", "admin")

	type want struct {
		code  int
		actor string // body when 200
	}
	cases := []struct {
		name, path, tok string
		want            want
	}{
		// read token: read ok (actor=name), deploy/admin forbidden
		{"read→read", "/t/read", readTok, want{200, "reader"}},
		{"read→deploy", "/t/deploy", readTok, want{http.StatusForbidden, ""}},
		{"read→admin", "/t/admin", readTok, want{http.StatusForbidden, ""}},
		// deploy token: read+deploy ok, admin forbidden
		{"deploy→read", "/t/read", deployTok, want{200, "deployer"}},
		{"deploy→deploy", "/t/deploy", deployTok, want{200, "deployer"}},
		{"deploy→admin", "/t/admin", deployTok, want{http.StatusForbidden, ""}},
		// admin token: everything ok
		{"admin→admin", "/t/admin", adminTok, want{200, "boss"}},
		// static bootstrap bearer: admin everywhere, no name → "operator"
		{"static→admin", "/t/admin", testToken, want{200, "operator"}},
		// missing / bad token: 401
		{"none→read", "/t/read", "", want{http.StatusUnauthorized, ""}},
		{"bad→read", "/t/read", "garbage", want{http.StatusUnauthorized, ""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, bearerReq(http.MethodGet, c.path, c.tok))
			if w.Code != c.want.code {
				t.Fatalf("code = %d, want %d (%s)", w.Code, c.want.code, w.Body.String())
			}
			if c.want.code == 200 && w.Body.String() != c.want.actor {
				t.Errorf("actor = %q, want %q", w.Body.String(), c.want.actor)
			}
		})
	}
}

func TestRequireRole_revokedTokenDenied(t *testing.T) {
	srv := newHTTPTestServer(t)
	srv.mux.HandleFunc("GET /t/read", srv.requireRole(RoleRead, echoActor))

	tok := mintToken(t, srv, "id-x", "temp", "admin")
	if err := srv.ledger.RevokeControlToken(context.Background(), "id-x"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, bearerReq(http.MethodGet, "/t/read", tok))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token code = %d, want 401", w.Code)
	}
}
