//go:build integration

package integration

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// requireDocker skips the test unless a usable Docker daemon and the git CLI
// are present. These tests deploy real containers, so without them there is
// nothing meaningful to run.
func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Skipf("docker daemon not available: %v", err)
	}
}

// repoRoot walks up from the test's working directory to the module root (the
// directory containing go.mod).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod above test dir")
		}
		dir = parent
	}
}

// buildBinary compiles the shuttle CLI once into a temp dir and returns its
// path. Built without -race (it is a child process; the race detector covers
// the test process itself).
func buildBinary(t *testing.T, root string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "shuttle")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/shuttle")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build shuttle: %v\n%s", err, out)
	}
	return bin
}

// freePort asks the kernel for an unused TCP port on the loopback interface.
// There is an inherent TOCTOU window before the port is rebound, acceptable for
// a local single-process test.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// lockedBuffer is a goroutine-safe buffer used to capture a subprocess's
// combined output so it can be dumped if the test fails.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startProc launches the shuttle binary with args, wiring its output to a
// buffer that is dumped to the test log on failure. The process is killed when
// ctx is cancelled (t.Context is cancelled during cleanup).
func startProc(ctx context.Context, t *testing.T, name, bin string, args ...string) {
	t.Helper()
	out := &lockedBuffer{}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("---- %s output ----\n%s", name, out.String())
		}
		// ctx cancellation already signalled the process; reap it so the test
		// doesn't leak a zombie.
		_ = cmd.Wait()
	})
}

// httpDo issues a request with the bearer token and returns status + body.
func httpDo(t *testing.T, method, url, bearer string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, ""
	}
	defer func() { _ = resp.Body.Close() }()
	var b bytes.Buffer
	_, _ = b.ReadFrom(resp.Body)
	return resp.StatusCode, b.String()
}

// waitFor polls fn until it returns true or the deadline elapses, failing the
// test with msg on timeout.
func waitFor(t *testing.T, timeout time.Duration, msg string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, msg)
}

// dockerRemoveE2EContainers force-removes any container this suite created,
// identified by the shuttle-e2e label baked into the test compose file.
func dockerRemoveE2EContainers(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ids, err := exec.CommandContext(ctx, "docker", "ps", "-aq", "--filter", "label=shuttle-e2e=1").Output()
	if err != nil {
		t.Logf("list e2e containers: %v", err)
		return
	}
	for _, id := range bytes.Fields(ids) {
		_ = exec.CommandContext(ctx, "docker", "rm", "-f", string(id)).Run()
	}
}

// writeIaCRepo scaffolds a minimal shuttle IaC repo as a git repo and returns
// its path. The single service "web" runs traefik/whoami, publishing it on
// webPort so the test can curl it. update_policy is recreate (rolling forbids a
// fixed published host port); no env_schema/domains, so no secrets provider or
// Caddy is needed.
func writeIaCRepo(t *testing.T, host string, webPort int) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "hosts.yaml"),
		fmt.Sprintf("hosts:\n  - name: %s\n", host))

	svcDir := filepath.Join(dir, "services", "web")
	mustWrite(t, filepath.Join(svcDir, "web.yaml"),
		fmt.Sprintf("name: web\nhost: %s\nupdate_policy: recreate\n", host))
	mustWrite(t, filepath.Join(svcDir, "docker-compose.yml"),
		fmt.Sprintf(`services:
  web:
    image: traefik/whoami:latest
    ports:
      - "127.0.0.1:%d:80"
    labels:
      - "shuttle-e2e=1"
    restart: "no"
`, webPort))

	gitInit(t, dir)
	return dir
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// gitInit turns dir into a committed git repo on branch main.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "e2e@shuttle.test")
	run("config", "user.name", "shuttle-e2e")
	run("add", "-A")
	run("commit", "-qm", "seed e2e repo")
}

// gitHead returns the tip commit SHA of the repo at dir.
func gitHead(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	return string(bytes.TrimSpace(out))
}

// dockerPull pre-fetches an image so the first deploy isn't dominated by (or
// flaky on) the pull. A pull failure skips the test — it usually means no
// network in the sandbox, not a shuttle bug.
func dockerPull(t *testing.T, image string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "pull", image).CombinedOutput(); err != nil {
		t.Skipf("cannot pull %s (no network?): %v\n%s", image, err, out)
	}
}
