//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestServiceBackupRestoreRoundTrip proves a service's data survives a
// backup→wipe→restore cycle through the real control plane and a real Docker
// daemon: deploy a service with a named volume, write a marker into the volume,
// POST /backup (volume engine, local store), confirm the ledger records success
// and a tar artifact lands on disk, delete the marker, POST /restore, and
// confirm the marker reappears.
func TestServiceBackupRestoreRoundTrip(t *testing.T) {
	requireDocker(t)
	dockerPull(t, "alpine:3")
	t.Cleanup(func() { dockerRemoveE2EContainers(t) })

	const (
		host   = "e2e-host"
		bearer = "e2e-bearer"
		marker = "shuttle-backup-marker-42"
	)
	root := repoRoot(t)
	bin := buildBinary(t, root)

	grpcPort := freePort(t)
	httpPort := freePort(t)
	base := fmt.Sprintf("http://127.0.0.1:%d", httpPort)

	backupDir := t.TempDir()
	iac := writeBackupIaCRepo(t, host)
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
backups:
  default_store: local
  default_target: %s
  poll_interval: 1h
`, bearer, grpcPort, httpPort, dataDir, iac, backupDir))

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

	// Deploy the store service.
	deployURL := fmt.Sprintf("%s/deploy/store?sha=%s", base, sha)
	waitFor(t, 45*time.Second, "deploy accepted", func() bool {
		code, _ := httpDo(t, http.MethodPost, deployURL, bearer)
		return code == http.StatusAccepted
	})
	waitFor(t, 90*time.Second, "store container running", func() bool {
		return storeContainerID(t) != ""
	})

	// Write a marker into the volume via the running container.
	if err := dockerExec(t, storeContainerID(t), "sh", "-c", "echo "+marker+" > /data/marker"); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	// Trigger a backup and wait for the ledger to record success.
	code, body := httpDo(t, http.MethodPost, base+"/backup/store", bearer)
	if code != http.StatusAccepted {
		t.Fatalf("POST /backup/store: want 202, got %d: %s", code, body)
	}
	waitFor(t, 90*time.Second, "backup success in ledger", func() bool {
		return latestBackupStatus(t, base, bearer, "store") == "success"
	})

	// A tar artifact must exist under the local target directory.
	if !hasTarArtifact(t, backupDir) {
		t.Fatalf("no .tar artifact under %s after backup", backupDir)
	}

	// Wipe the marker, then restore and confirm it comes back.
	if err := dockerExec(t, storeContainerID(t), "sh", "-c", "rm -f /data/marker"); err != nil {
		t.Fatalf("delete marker: %v", err)
	}
	code, body = httpDo(t, http.MethodPost, base+"/restore?service=store", bearer)
	if code != http.StatusAccepted {
		t.Fatalf("POST /restore: want 202, got %d: %s", code, body)
	}
	waitFor(t, 90*time.Second, "marker restored", func() bool {
		cid := storeContainerID(t) // changes after the restore restart
		if cid == "" {
			return false
		}
		out, err := dockerExecOut(t, cid, "sh", "-c", "cat /data/marker 2>/dev/null")
		return err == nil && strings.TrimSpace(out) == marker
	})
}

// writeBackupIaCRepo scaffolds a repo whose single service "store" runs an idle
// alpine container with a named volume and a backup policy (engine inherited as
// volume; store/target inherited from the orchestrator's backups defaults).
func writeBackupIaCRepo(t *testing.T, host string) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "hosts.yaml"), fmt.Sprintf("hosts:\n  - name: %s\n", host))
	svcDir := filepath.Join(dir, "services", "store")
	mustWrite(t, filepath.Join(svcDir, "store.yaml"), fmt.Sprintf(`name: store
host: %s
update_policy: recreate
backup:
  engine: volume
`, host))
	mustWrite(t, filepath.Join(svcDir, "docker-compose.yml"), `services:
  store:
    image: alpine:3
    command: ["tail", "-f", "/dev/null"]
    volumes:
      - data:/data
    labels:
      - "shuttle-e2e=1"
    restart: "no"
volumes:
  data:
`)
	gitInit(t, dir)
	return dir
}

// storeContainerID returns the running store container id (empty if none).
func storeContainerID(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "ps", "-q",
		"--filter", "label=shuttle-e2e=1",
		"--filter", "label=com.docker.compose.service=store").Output()
	if err != nil {
		return ""
	}
	if i := bytes.IndexByte(out, '\n'); i >= 0 {
		out = out[:i]
	}
	return string(bytes.TrimSpace(out))
}

func dockerExec(t *testing.T, cid string, args ...string) error {
	t.Helper()
	_, err := dockerExecOut(t, cid, args...)
	return err
}

func dockerExecOut(t *testing.T, cid string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	full := append([]string{"exec", cid}, args...)
	out, err := exec.CommandContext(ctx, "docker", full...).CombinedOutput()
	return string(out), err
}

// latestBackupStatus returns the status of the newest backup for a service.
func latestBackupStatus(t *testing.T, base, bearer, service string) string {
	t.Helper()
	code, body := httpDo(t, http.MethodGet, base+"/backups?service="+service, bearer)
	if code != http.StatusOK {
		return ""
	}
	var rows []struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(body), &rows); err != nil || len(rows) == 0 {
		return ""
	}
	return rows[0].Status // ListBackups returns newest first
}

// hasTarArtifact reports whether any .tar file exists under dir.
func hasTarArtifact(t *testing.T, dir string) bool {
	t.Helper()
	found := false
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".tar") {
			found = true
		}
		return nil
	})
	return found
}
