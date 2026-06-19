//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

// TestRBACEnforcesRoles proves role-scoped tokens are enforced end-to-end:
// the admin bearer mints a read-only token, which may list deploys but is
// forbidden (403) from deploying or minting further tokens.
func TestRBACEnforcesRoles(t *testing.T) {
	requireDocker(t) // not strictly needed, but keeps RBAC test in the gated suite

	const admin = "admin-bearer"
	root := repoRoot(t)
	bin := buildBinary(t, root)

	grpcPort := freePort(t)
	httpPort := freePort(t)
	base := fmt.Sprintf("http://127.0.0.1:%d", httpPort)

	cfg := filepath.Join(t.TempDir(), "config.yml")
	writeFileOrFail(t, cfg, fmt.Sprintf(`bearer_token: %s
grpc_addr: "127.0.0.1:%d"
http_addr: "127.0.0.1:%d"
data_dir: %s
`, admin, grpcPort, httpPort, t.TempDir()))

	ctx := t.Context()
	startProc(ctx, t, "orchestrator", bin, "orchestrator", "--config", cfg)
	waitFor(t, 30*time.Second, "orchestrator /healthz", func() bool {
		code, _ := httpDo(t, http.MethodGet, base+"/healthz", "")
		return code == http.StatusOK
	})

	// Admin mints a read-only token.
	code, body := httpPostJSON(t, base+"/tokens", admin, `{"name":"reader","role":"read"}`)
	if code != http.StatusCreated {
		t.Fatalf("create read token: want 201, got %d: %s", code, body)
	}
	var created struct {
		Token string `json:"token"`
		Role  string `json:"role"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if created.Role != "read" || created.Token == "" {
		t.Fatalf("unexpected token: %+v", created)
	}
	readTok := created.Token

	// read token CAN list deploys.
	if c, b := httpDo(t, http.MethodGet, base+"/deploys", readTok); c != http.StatusOK {
		t.Fatalf("read token GET /deploys: want 200, got %d: %s", c, b)
	}

	// read token CANNOT deploy (deploy role) → 403.
	if c, b := httpDo(t, http.MethodPost, base+"/deploy/web?sha=abc123", readTok); c != http.StatusForbidden {
		t.Fatalf("read token POST /deploy: want 403, got %d: %s", c, b)
	}

	// read token CANNOT mint tokens (admin role) → 403.
	if c, b := httpPostJSON(t, base+"/tokens", readTok, `{"name":"x","role":"read"}`); c != http.StatusForbidden {
		t.Fatalf("read token POST /tokens: want 403, got %d: %s", c, b)
	}

	// No token at all → 401.
	if c, _ := httpDo(t, http.MethodGet, base+"/deploys", ""); c != http.StatusUnauthorized {
		t.Fatalf("no token GET /deploys: want 401, got %d", c)
	}
}

// httpPostJSON issues an authed POST with a JSON body and returns status + body.
func httpPostJSON(t *testing.T, url, bearer, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, ""
	}
	defer func() { _ = resp.Body.Close() }()
	var b bytes.Buffer
	_, _ = io.Copy(&b, resp.Body)
	return resp.StatusCode, b.String()
}
