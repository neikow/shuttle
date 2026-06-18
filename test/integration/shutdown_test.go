//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestReadyzAndGracefulShutdown verifies the orchestrator advertises readiness
// once it is serving and exits cleanly (exit 0) on SIGTERM rather than being
// killed — proving the graceful-shutdown path drains instead of hanging or
// crashing. No containers are deployed, so this runs even without Docker.
func TestReadyzAndGracefulShutdown(t *testing.T) {
	root := repoRoot(t)
	bin := buildBinary(t, root)

	grpcPort := freePort(t)
	httpPort := freePort(t)
	httpBase := fmt.Sprintf("http://127.0.0.1:%d", httpPort)

	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	writeFileOrFail(t, cfgPath, fmt.Sprintf(`bearer_token: e2e-bearer
grpc_addr: "127.0.0.1:%d"
http_addr: "127.0.0.1:%d"
data_dir: %s
`, grpcPort, httpPort, t.TempDir()))

	// Manage the process directly so we can send SIGTERM and inspect the exit
	// code (startProc kills via context, which would mask a graceful exit).
	out := &lockedBuffer{}
	cmd := exec.CommandContext(t.Context(), bin, "orchestrator", "--config", cfgPath)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start orchestrator: %v", err)
	}
	exited := false
	t.Cleanup(func() {
		if !exited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		if t.Failed() {
			t.Logf("---- orchestrator output ----\n%s", out.String())
		}
	})

	// /healthz (liveness) up, then /readyz (readiness) flips to 200.
	waitFor(t, 30*time.Second, "orchestrator /healthz", func() bool {
		code, _ := httpDo(t, http.MethodGet, httpBase+"/healthz", "")
		return code == http.StatusOK
	})
	waitFor(t, 15*time.Second, "orchestrator /readyz to become ready", func() bool {
		code, _ := httpDo(t, http.MethodGet, httpBase+"/readyz", "")
		return code == http.StatusOK
	})

	// SIGTERM must yield a clean (exit 0), prompt shutdown.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal SIGTERM: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		exited = true
		if err != nil {
			t.Fatalf("orchestrator exited non-zero on SIGTERM: %v\n%s", err, out.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("orchestrator did not exit within 15s of SIGTERM\n%s", out.String())
	}
}
