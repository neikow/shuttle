//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestWebhookRateLimit verifies the unauthenticated /webhook endpoint is
// IP-rate-limited: a burst of requests from one client eventually returns 429,
// while earlier ones are still processed (here rejected with an auth/4xx error,
// not throttled) — proving the limiter is selective, not a blanket block.
func TestWebhookRateLimit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := repoRoot(t)
	bin := buildBinary(t, root)

	grpcPort := freePort(t)
	httpPort := freePort(t)
	httpBase := fmt.Sprintf("http://127.0.0.1:%d", httpPort)

	// A real (committed) repo so the webhook endpoints are registered; no agent
	// and no valid signatures, so nothing actually deploys.
	iac := writeIaCRepo(t, "e2e-host", freePort(t))

	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	writeFileOrFail(t, cfgPath, fmt.Sprintf(`bearer_token: e2e-bearer
grpc_addr: "127.0.0.1:%d"
http_addr: "127.0.0.1:%d"
data_dir: %s
repo_url: %s
repo_branch: main
webhook_secret: e2e-webhook
webhook_rate_limit_per_minute: 60
`, grpcPort, httpPort, t.TempDir(), iac))

	startProc(t.Context(), t, "orchestrator", bin, "orchestrator", "--config", cfgPath)
	waitFor(t, 30*time.Second, "orchestrator /healthz", func() bool {
		code, _ := httpDo(t, http.MethodGet, httpBase+"/healthz", "")
		return code == http.StatusOK
	})

	// Hammer /webhook from one client. With 60/min (burst 10), a tight burst of
	// 40 must produce both throttled (429) and non-throttled responses.
	var got429, gotOther int
	for range 40 {
		code, _ := httpDo(t, http.MethodPost, httpBase+"/webhook", "")
		if code == http.StatusTooManyRequests {
			got429++
		} else {
			gotOther++
		}
	}
	if got429 == 0 {
		t.Errorf("expected some 429s under a 40-request burst, got none (other=%d)", gotOther)
	}
	if gotOther == 0 {
		t.Errorf("expected some non-429 responses (limiter should be selective), got none")
	}
	t.Logf("burst of 40: %d throttled (429), %d passed to handler", got429, gotOther)
}
