//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

// TestAuditTrailRecordsDeploy proves a manual deploy lands in the audit log:
// deploy a service through the control plane, then GET /audit and confirm a
// deploy/success entry naming the service and the default "operator" actor.
func TestAuditTrailRecordsDeploy(t *testing.T) {
	requireDocker(t)
	dockerPull(t, "traefik/whoami:latest")
	t.Cleanup(func() { dockerRemoveE2EContainers(t) })

	const (
		host   = "e2e-host"
		bearer = "e2e-bearer"
	)
	root := repoRoot(t)
	bin := buildBinary(t, root)

	grpcPort := freePort(t)
	httpPort := freePort(t)
	webPort := freePort(t)
	base := fmt.Sprintf("http://127.0.0.1:%d", httpPort)

	iac := writeIaCRepo(t, host, webPort)
	sha := gitHead(t, iac)
	dataDir := t.TempDir()

	cfg := filepath.Join(t.TempDir(), "config.yml")
	writeFileOrFail(t, cfg, fmt.Sprintf(`bearer_token: %s
grpc_addr: "127.0.0.1:%d"
http_addr: "127.0.0.1:%d"
data_dir: %s
repo_url: %s
repo_branch: main
webhook_secret: e2e-webhook
secrets_provider: none
`, bearer, grpcPort, httpPort, dataDir, iac))

	ctx := t.Context()

	startProc(ctx, t, "orchestrator", bin, "orchestrator", "--config", cfg)
	waitFor(t, 30*time.Second, "orchestrator /healthz", func() bool {
		code, _ := httpDo(t, http.MethodGet, base+"/healthz", "")
		return code == http.StatusOK
	})

	startProc(ctx, t, "agent", bin, "agent",
		"--orchestrator", fmt.Sprintf("127.0.0.1:%d", grpcPort),
		"--host", host, "--work-dir", t.TempDir(),
	)

	deployURL := fmt.Sprintf("%s/deploy/web?sha=%s", base, sha)
	waitFor(t, 45*time.Second, "deploy accepted", func() bool {
		code, _ := httpDo(t, http.MethodPost, deployURL, bearer)
		return code == http.StatusAccepted
	})
	waitFor(t, 90*time.Second, "ledger to record success", func() bool {
		return hasSuccessfulDeploy(t, base, bearer, "web")
	})

	// The accepted deploy must have produced a deploy/success audit entry.
	code, body := httpDo(t, http.MethodGet, base+"/audit?action=deploy", bearer)
	if code != http.StatusOK {
		t.Fatalf("GET /audit: want 200, got %d: %s", code, body)
	}
	var entries []struct {
		Actor  string `json:"actor"`
		Action string `json:"action"`
		Target string `json:"target"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &entries); err != nil {
		t.Fatalf("decode audit: %v\n%s", err, body)
	}
	found := false
	for _, e := range entries {
		if e.Action == "deploy" && e.Target == "web" && e.Result == "success" {
			if e.Actor != "operator" {
				t.Errorf("audit actor = %q, want operator", e.Actor)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("no deploy/success audit entry for web: %s", body)
	}
}
