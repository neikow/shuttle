//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestBackupRestoreRoundTrip proves deploy history survives a backup/restore
// cycle: deploy a service against orchestrator A, `shuttle backup` its ledger,
// `shuttle restore` into a fresh data dir, then bring up orchestrator B on the
// restored ledger and confirm it serves the same deploy record.
func TestBackupRestoreRoundTrip(t *testing.T) {
	requireDocker(t)
	dockerPull(t, "traefik/whoami:latest")
	t.Cleanup(func() { dockerRemoveE2EContainers(t) })

	const (
		host   = "e2e-host"
		bearer = "e2e-bearer"
	)
	root := repoRoot(t)
	bin := buildBinary(t, root)

	grpcA := freePort(t)
	httpA := freePort(t)
	webPort := freePort(t)
	baseA := fmt.Sprintf("http://127.0.0.1:%d", httpA)

	iac := writeIaCRepo(t, host, webPort)
	sha := gitHead(t, iac)
	dataA := t.TempDir()

	cfgA := filepath.Join(t.TempDir(), "a.yml")
	writeFileOrFail(t, cfgA, fmt.Sprintf(`bearer_token: %s
grpc_addr: "127.0.0.1:%d"
http_addr: "127.0.0.1:%d"
data_dir: %s
repo_url: %s
repo_branch: main
webhook_secret: e2e-webhook
secrets_provider: none
`, bearer, grpcA, httpA, dataA, iac))

	ctx := t.Context()

	startProc(ctx, t, "orchestrator-A", bin, "orchestrator", "--config", cfgA)
	waitFor(t, 30*time.Second, "orchestrator A /healthz", func() bool {
		code, _ := httpDo(t, http.MethodGet, baseA+"/healthz", "")
		return code == http.StatusOK
	})

	startProc(ctx, t, "agent", bin, "agent",
		"--orchestrator", fmt.Sprintf("127.0.0.1:%d", grpcA),
		"--host", host, "--work-dir", t.TempDir(),
	)

	deployURL := fmt.Sprintf("%s/deploy/web?sha=%s", baseA, sha)
	waitFor(t, 45*time.Second, "deploy accepted", func() bool {
		code, _ := httpDo(t, http.MethodPost, deployURL, bearer)
		return code == http.StatusAccepted
	})
	waitFor(t, 90*time.Second, "ledger A to record success", func() bool {
		return hasSuccessfulDeploy(t, baseA, bearer, "web")
	})

	// Back up the live ledger, then restore it into a fresh data dir.
	backupFile := filepath.Join(t.TempDir(), "snapshot.db")
	runShuttle(t, bin, "backup", "--data-dir", dataA, "--out", backupFile)
	if _, err := os.Stat(backupFile); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}
	dataB := t.TempDir()
	runShuttle(t, bin, "restore", "--data-dir", dataB, backupFile)

	// Orchestrator B serves only from the restored ledger (no repo configured).
	grpcB := freePort(t)
	httpB := freePort(t)
	baseB := fmt.Sprintf("http://127.0.0.1:%d", httpB)
	cfgB := filepath.Join(t.TempDir(), "b.yml")
	writeFileOrFail(t, cfgB, fmt.Sprintf(`bearer_token: %s
grpc_addr: "127.0.0.1:%d"
http_addr: "127.0.0.1:%d"
data_dir: %s
`, bearer, grpcB, httpB, dataB))

	startProc(ctx, t, "orchestrator-B", bin, "orchestrator", "--config", cfgB)
	waitFor(t, 30*time.Second, "orchestrator B /healthz", func() bool {
		code, _ := httpDo(t, http.MethodGet, baseB+"/healthz", "")
		return code == http.StatusOK
	})

	if !hasSuccessfulDeploy(t, baseB, bearer, "web") {
		t.Fatal("restored orchestrator B is missing the web deploy from the backup")
	}
}

// hasSuccessfulDeploy reports whether /deploys on baseURL shows a successful
// deploy of service.
func hasSuccessfulDeploy(t *testing.T, baseURL, bearer, service string) bool {
	t.Helper()
	code, body := httpDo(t, http.MethodGet, baseURL+"/deploys?service="+service, bearer)
	if code != http.StatusOK {
		return false
	}
	var deploys []struct {
		Service string
		Status  string
	}
	if err := json.Unmarshal([]byte(body), &deploys); err != nil {
		return false
	}
	for _, d := range deploys {
		if d.Service == service && d.Status == "success" {
			return true
		}
	}
	return false
}

func writeFileOrFail(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
