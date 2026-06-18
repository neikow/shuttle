//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDeployResolvesFileSecrets proves end-to-end secret resolution: the file
// secrets provider reads a dotenv file on the orchestrator, renderEnv ships the
// declared env_schema key with the deploy, compose interpolates it, and the
// container reflects it.
//
// traefik/whoami prints "Name: <WHOAMI_NAME>" when that env var is set, so the
// secret value surfacing in the HTTP response is direct proof the value flowed
// secret file → orchestrator render → agent compose → running container.
func TestDeployResolvesFileSecrets(t *testing.T) {
	requireDocker(t)
	dockerPull(t, "traefik/whoami:latest")
	t.Cleanup(func() { dockerRemoveE2EContainers(t) })

	const (
		host       = "e2e-host"
		bearer     = "e2e-bearer"
		env        = "prod"
		secretVal  = "shuttle-file-secret-ok"
		secretName = "WHOAMI_NAME"
	)
	root := repoRoot(t)
	bin := buildBinary(t, root)

	grpcPort := freePort(t)
	httpPort := freePort(t)
	webPort := freePort(t)
	httpBase := fmt.Sprintf("http://127.0.0.1:%d", httpPort)

	iac := writeIaCRepoWithSecret(t, host, webPort, secretName, env)
	sha := gitHead(t, iac)

	// Lay out the secret as the file provider expects:
	//   <secretsDir>/<env>/services/<service>.env
	secretsDir := t.TempDir()
	svcSecretFile := filepath.Join(secretsDir, env, "services", "web.env")
	mustWrite(t, svcSecretFile, fmt.Sprintf("%s=%s\n", secretName, secretVal))

	dataDir := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	cfg := fmt.Sprintf(`bearer_token: %s
grpc_addr: "127.0.0.1:%d"
http_addr: "127.0.0.1:%d"
data_dir: %s
repo_url: %s
repo_branch: main
webhook_secret: e2e-webhook
secrets_provider: file
`, bearer, grpcPort, httpPort, dataDir, iac)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	ctx := t.Context()

	startProcEnv(ctx, t, "orchestrator", bin,
		[]string{"SHUTTLE_SECRETS_DIR=" + secretsDir},
		"orchestrator", "--config", cfgPath,
	)
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

	deployURL := fmt.Sprintf("%s/deploy/web?sha=%s", httpBase, sha)
	waitFor(t, 45*time.Second, "deploy to be accepted", func() bool {
		code, body := httpDo(t, http.MethodPost, deployURL, bearer)
		if code == http.StatusAccepted {
			return true
		}
		t.Logf("deploy not yet accepted: code=%d body=%s", code, strings.TrimSpace(body))
		return false
	})

	// The container must serve, and its body must carry the resolved secret.
	webURL := fmt.Sprintf("http://127.0.0.1:%d/", webPort)
	waitFor(t, 120*time.Second, "whoami to serve the resolved secret", func() bool {
		resp, err := http.Get(webURL)
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return false
		}
		buf := make([]byte, 1024)
		n, _ := resp.Body.Read(buf)
		return strings.Contains(string(buf[:n]), "Name: "+secretVal)
	})
}

// writeIaCRepoWithSecret scaffolds an IaC repo whose "web" service declares
// env_schema: [schemaKey] and reads it from env_from: <envFrom>. The compose
// interpolates the key into WHOAMI_NAME so the container reflects the value.
func writeIaCRepoWithSecret(t *testing.T, host string, webPort int, schemaKey, envFrom string) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "hosts.yaml"),
		fmt.Sprintf("hosts:\n  - name: %s\n", host))

	svcDir := filepath.Join(dir, "services", "web")
	mustWrite(t, filepath.Join(svcDir, "web.yaml"),
		fmt.Sprintf("name: web\nhost: %s\nupdate_policy: recreate\nenv_from: %s\nenv_schema:\n  - %s\n",
			host, envFrom, schemaKey))
	mustWrite(t, filepath.Join(svcDir, "docker-compose.yml"),
		fmt.Sprintf(`services:
  web:
    image: traefik/whoami:latest
    environment:
      WHOAMI_NAME: ${%s}
    ports:
      - "127.0.0.1:%d:80"
    labels:
      - "shuttle-e2e=1"
    restart: "no"
`, schemaKey, webPort))

	gitInit(t, dir)
	return dir
}
