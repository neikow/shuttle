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

// TestDeployLogsCaptured proves the orchestrator persists the agent's deploy
// output: deploy a service, then GET /deploys/{id}/logs and confirm non-empty
// captured lines for that deploy.
func TestDeployLogsCaptured(t *testing.T) {
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

	// Find the successful deploy's id.
	code, body := httpDo(t, http.MethodGet, base+"/deploys?service=web", bearer)
	if code != http.StatusOK {
		t.Fatalf("GET /deploys: want 200, got %d: %s", code, body)
	}
	var deploys []struct {
		DeployID string `json:"DeployID"`
		Status   string `json:"Status"`
	}
	if err := json.Unmarshal([]byte(body), &deploys); err != nil {
		t.Fatalf("decode deploys: %v\n%s", err, body)
	}
	var deployID string
	for _, d := range deploys {
		if d.Status == "success" {
			deployID = d.DeployID
			break
		}
	}
	if deployID == "" {
		t.Fatalf("no successful deploy id in: %s", body)
	}

	// Its logs must have been captured.
	code, body = httpDo(t, http.MethodGet, base+"/deploys/"+deployID+"/logs", bearer)
	if code != http.StatusOK {
		t.Fatalf("GET /deploys/{id}/logs: want 200, got %d: %s", code, body)
	}
	var logs []struct {
		Stream string `json:"stream"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal([]byte(body), &logs); err != nil {
		t.Fatalf("decode logs: %v\n%s", err, body)
	}
	if len(logs) == 0 {
		t.Fatalf("no captured logs for deploy %s", deployID)
	}

	// A read-tier caller can fetch logs; an unauthenticated one cannot.
	if code, _ := httpDo(t, http.MethodGet, base+"/deploys/"+deployID+"/logs", ""); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated logs: want 401, got %d", code)
	}
}
