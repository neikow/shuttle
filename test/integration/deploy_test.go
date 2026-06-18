//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDeployServesAndRecords drives the full control plane end to end against a
// real Docker daemon:
//
//	build binary → start orchestrator (git sync, insecure gRPC) → start agent →
//	POST /deploy → agent runs `docker compose up` → container serves HTTP →
//	ledger records the deploy as success.
//
// It is the executable proof of the core invariant "orchestrator renders,
// agent runs compose, the result is recorded."
func TestDeployServesAndRecords(t *testing.T) {
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
	httpBase := fmt.Sprintf("http://127.0.0.1:%d", httpPort)

	iac := writeIaCRepo(t, host, webPort)
	sha := gitHead(t, iac)
	dataDir := t.TempDir()

	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	cfg := fmt.Sprintf(`bearer_token: %s
grpc_addr: "127.0.0.1:%d"
http_addr: "127.0.0.1:%d"
data_dir: %s
repo_url: %s
repo_branch: main
webhook_secret: e2e-webhook
secrets_provider: none
`, bearer, grpcPort, httpPort, dataDir, iac)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	ctx := t.Context() // cancelled during cleanup → kills the subprocesses

	startProc(ctx, t, "orchestrator", bin, "orchestrator", "--config", cfgPath)
	waitFor(t, 30*time.Second, "orchestrator /healthz", func() bool {
		code, _ := httpDo(t, http.MethodGet, httpBase+"/healthz", "")
		return code == http.StatusOK
	})

	agentWork := t.TempDir()
	startProc(ctx, t, "agent", bin, "agent",
		"--orchestrator", fmt.Sprintf("127.0.0.1:%d", grpcPort),
		"--host", host,
		"--work-dir", agentWork,
	)

	// Trigger the deploy, retrying until the agent has registered: until then
	// dispatch can't reach the host and /deploy returns a non-2xx.
	deployURL := fmt.Sprintf("%s/deploy/web?sha=%s", httpBase, sha)
	waitFor(t, 45*time.Second, "deploy to be accepted (agent connected)", func() bool {
		code, body := httpDo(t, http.MethodPost, deployURL, bearer)
		if code == http.StatusAccepted {
			return true
		}
		t.Logf("deploy not yet accepted: code=%d body=%s", code, strings.TrimSpace(body))
		return false
	})

	// The container must come up and actually serve. whoami answers 200 with a
	// body containing "Hostname:".
	webURL := fmt.Sprintf("http://127.0.0.1:%d/", webPort)
	waitFor(t, 120*time.Second, "whoami container to serve", func() bool {
		resp, err := http.Get(webURL)
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return false
		}
		var sb strings.Builder
		buf := make([]byte, 512)
		n, _ := resp.Body.Read(buf)
		sb.Write(buf[:n])
		return strings.Contains(sb.String(), "Hostname:")
	})

	// The ledger must record the deploy as success.
	waitFor(t, 60*time.Second, "ledger to record deploy success", func() bool {
		code, body := httpDo(t, http.MethodGet, httpBase+"/deploys?service=web", bearer)
		if code != http.StatusOK {
			return false
		}
		var deploys []struct {
			Service string
			Status  string
		}
		if err := json.Unmarshal([]byte(body), &deploys); err != nil {
			t.Logf("decode /deploys: %v (body=%s)", err, body)
			return false
		}
		for _, d := range deploys {
			if d.Service == "web" && d.Status == "success" {
				return true
			}
		}
		return false
	})
}
