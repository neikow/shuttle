//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestUIMutationsRoleMatrix proves the role matrix the role-gated UI relies on,
// end-to-end against the real binary: GET /whoami reports the caller's role, a
// read token may inspect but not mutate, a deploy token may run operational
// mutations (prune/rollback) but no admin ones, and an admin may manage tokens,
// repo webhooks, and agent enrollment. The orchestrator is wired to a git IaC
// repo so the repo-webhook and enrollment endpoints are active; no Docker is
// needed for the auth-matrix assertions (only git, for the repo clone).
func TestUIMutationsRoleMatrix(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	const (
		admin = "admin-bearer"
		host  = "web-1"
	)
	root := repoRoot(t)
	bin := buildBinary(t, root)

	iac := writeIaCRepo(t, host, freePort(t)) // declares host web-1 + service web

	grpcPort := freePort(t)
	httpPort := freePort(t)
	base := fmt.Sprintf("http://127.0.0.1:%d", httpPort)

	cfg := filepath.Join(t.TempDir(), "config.yml")
	writeFileOrFail(t, cfg, fmt.Sprintf(`bearer_token: %s
grpc_addr: "127.0.0.1:%d"
http_addr: "127.0.0.1:%d"
data_dir: %s
repo_url: "file://%s"
repo_branch: main
webhook_secret: test-webhook-secret
agent_token_auth: true
`, admin, grpcPort, httpPort, t.TempDir(), iac))

	ctx := t.Context()
	startProc(ctx, t, "orchestrator", bin, "orchestrator", "--config", cfg)
	waitFor(t, 30*time.Second, "orchestrator /healthz", func() bool {
		code, _ := httpDo(t, http.MethodGet, base+"/healthz", "")
		return code == http.StatusOK
	})
	// The repo-webhook and enrollment endpoints come up after the first repo
	// sync; wait until /webhooks/repo answers (200) for the admin bearer.
	waitFor(t, 30*time.Second, "repo endpoints ready", func() bool {
		code, _ := httpDo(t, http.MethodGet, base+"/webhooks/repo", admin)
		return code == http.StatusOK
	})

	// --- static bearer is admin -------------------------------------------
	if got := whoami(t, base, admin); got.Role != "admin" || got.Name != "" {
		t.Fatalf("static bearer whoami = %+v, want {name:\"\" role:admin}", got)
	}

	// --- admin mints one token per role -----------------------------------
	readTok := mintRoleToken(t, base, admin, "reader", "read")
	deployTok := mintRoleToken(t, base, admin, "deployer", "deploy")
	adminTok := mintRoleToken(t, base, admin, "boss", "admin")

	for _, c := range []struct {
		tok, name, role string
	}{
		{readTok, "reader", "read"},
		{deployTok, "deployer", "deploy"},
		{adminTok, "boss", "admin"},
	} {
		if got := whoami(t, base, c.tok); got.Role != c.role || got.Name != c.name {
			t.Fatalf("whoami(%s) = %+v, want {name:%q role:%q}", c.name, got, c.name, c.role)
		}
	}

	// --- read token: inspect yes, mutate no -------------------------------
	if c, b := httpDo(t, http.MethodGet, base+"/deploys", readTok); c != http.StatusOK {
		t.Fatalf("read GET /deploys: want 200, got %d: %s", c, b)
	}
	for _, m := range []struct{ method, path string }{
		{http.MethodPost, "/prune"},
		{http.MethodPost, "/rollback?service=web"},
	} {
		if c, b := httpDo(t, m.method, base+m.path, readTok); c != http.StatusForbidden {
			t.Fatalf("read %s %s: want 403, got %d: %s", m.method, m.path, c, b)
		}
	}
	if c, b := httpPostJSON(t, base+"/tokens", readTok, `{"name":"x","role":"read"}`); c != http.StatusForbidden {
		t.Fatalf("read POST /tokens: want 403, got %d: %s", c, b)
	}
	if c, b := httpPostJSON(t, base+"/webhooks/repo", readTok, `{"service":"web"}`); c != http.StatusForbidden {
		t.Fatalf("read POST /webhooks/repo: want 403, got %d: %s", c, b)
	}
	if c, b := httpPostJSON(t, base+"/enroll", readTok, fmt.Sprintf(`{"host":%q}`, host)); c != http.StatusForbidden {
		t.Fatalf("read POST /enroll: want 403, got %d: %s", c, b)
	}

	// --- deploy token: operational yes, admin no --------------------------
	if c, b := httpDo(t, http.MethodPost, base+"/prune", deployTok); c != http.StatusOK {
		t.Fatalf("deploy POST /prune: want 200, got %d: %s", c, b)
	}
	// rollback with no deploy history is a 409 (no target) — the point is that
	// authorization passed (not 401/403).
	if c, b := httpDo(t, http.MethodPost, base+"/rollback?service=web", deployTok); c == http.StatusUnauthorized || c == http.StatusForbidden {
		t.Fatalf("deploy POST /rollback: want authz to pass, got %d: %s", c, b)
	}
	if c, b := httpPostJSON(t, base+"/tokens", deployTok, `{"name":"x","role":"read"}`); c != http.StatusForbidden {
		t.Fatalf("deploy POST /tokens: want 403, got %d: %s", c, b)
	}
	if c, b := httpPostJSON(t, base+"/webhooks/repo", deployTok, `{"service":"web"}`); c != http.StatusForbidden {
		t.Fatalf("deploy POST /webhooks/repo: want 403, got %d: %s", c, b)
	}

	// --- admin: full token / webhook / enrollment lifecycle ---------------
	// Token already created above; revoke it by id.
	tokenID := createdTokenID(t, base, admin, "to-revoke", "read")
	if c, b := httpDo(t, http.MethodDelete, base+"/tokens/"+tokenID, admin); c != http.StatusNoContent {
		t.Fatalf("admin DELETE /tokens/{id}: want 204, got %d: %s", c, b)
	}

	// Repo webhook create → list → delete.
	c, b := httpPostJSON(t, base+"/webhooks/repo", admin, `{"service":"web"}`)
	if c != http.StatusCreated {
		t.Fatalf("admin POST /webhooks/repo: want 201, got %d: %s", c, b)
	}
	var wh struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(b), &wh); err != nil || wh.ID == "" {
		t.Fatalf("decode webhook create: %v (%s)", err, b)
	}
	if c, b := httpDo(t, http.MethodDelete, base+"/webhooks/repo/"+wh.ID, admin); c != http.StatusNoContent {
		t.Fatalf("admin DELETE /webhooks/repo/{id}: want 204, got %d: %s", c, b)
	}

	// Agent enrollment: mint a single-use join token for the repo host.
	c, b = httpPostJSON(t, base+"/enroll", admin, fmt.Sprintf(`{"host":%q}`, host))
	if c != http.StatusCreated {
		t.Fatalf("admin POST /enroll: want 201, got %d: %s", c, b)
	}
	var enr struct {
		JoinToken string `json:"join_token"`
		Host      string `json:"host"`
	}
	if err := json.Unmarshal([]byte(b), &enr); err != nil || enr.JoinToken == "" || enr.Host != host {
		t.Fatalf("decode enroll: %v (%s)", err, b)
	}

	// --- no token → 401 on whoami -----------------------------------------
	if c, _ := httpDo(t, http.MethodGet, base+"/whoami", ""); c != http.StatusUnauthorized {
		t.Fatalf("no token GET /whoami: want 401, got %d", c)
	}
}

type whoAmI struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

func whoami(t *testing.T, base, bearer string) whoAmI {
	t.Helper()
	c, b := httpDo(t, http.MethodGet, base+"/whoami", bearer)
	if c != http.StatusOK {
		t.Fatalf("GET /whoami: want 200, got %d: %s", c, b)
	}
	var w whoAmI
	if err := json.Unmarshal([]byte(b), &w); err != nil {
		t.Fatalf("decode whoami: %v (%s)", err, b)
	}
	return w
}

// mintRoleToken creates a control token of the given role and returns its
// plaintext.
func mintRoleToken(t *testing.T, base, admin, name, role string) string {
	t.Helper()
	c, b := httpPostJSON(t, base+"/tokens", admin, fmt.Sprintf(`{"name":%q,"role":%q}`, name, role))
	if c != http.StatusCreated {
		t.Fatalf("create %s token: want 201, got %d: %s", role, c, b)
	}
	var created struct {
		Token string `json:"token"`
		Role  string `json:"role"`
	}
	if err := json.Unmarshal([]byte(b), &created); err != nil || created.Token == "" || created.Role != role {
		t.Fatalf("decode %s token: %v (%s)", role, err, b)
	}
	return created.Token
}

// createdTokenID creates a control token and returns its id (not the plaintext).
func createdTokenID(t *testing.T, base, admin, name, role string) string {
	t.Helper()
	c, b := httpPostJSON(t, base+"/tokens", admin, fmt.Sprintf(`{"name":%q,"role":%q}`, name, role))
	if c != http.StatusCreated {
		t.Fatalf("create token: want 201, got %d: %s", c, b)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(b), &created); err != nil || created.ID == "" {
		t.Fatalf("decode token id: %v (%s)", err, b)
	}
	return created.ID
}
