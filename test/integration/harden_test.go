//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

// TestHardenedEndpoints drives a real orchestrator with metrics_require_auth set
// and asserts the unauthenticated-surface hardening end-to-end: /metrics is
// gated (401 without a token, 200 with), and baseline security headers ride on
// responses (checked on the unauthenticated /healthz probe).
func TestHardenedEndpoints(t *testing.T) {
	const admin = "static-admin"
	root := repoRoot(t)
	bin := buildBinary(t, root)

	grpcPort := freePort(t)
	httpPort := freePort(t)
	base := fmt.Sprintf("http://127.0.0.1:%d", httpPort)

	cfg := filepath.Join(t.TempDir(), "config.yml")
	writeFileOrFail(t, cfg, fmt.Sprintf(`bearer_token: %s
grpc_addr: "127.0.0.1:%d"
http_addr: "127.0.0.1:%d"
data_dir: %s
metrics_require_auth: true
`, admin, grpcPort, httpPort, t.TempDir()))

	ctx := t.Context()
	startProc(ctx, t, "orchestrator", bin, "orchestrator", "--config", cfg)
	waitFor(t, 30*time.Second, "orchestrator /healthz", func() bool {
		code, _ := httpDo(t, http.MethodGet, base+"/healthz", "")
		return code == http.StatusOK
	})

	// /metrics gated: 401 without a token, 200 with the static bearer.
	if c, _ := httpDo(t, http.MethodGet, base+"/metrics", ""); c != http.StatusUnauthorized {
		t.Fatalf("no-token GET /metrics: want 401, got %d", c)
	}
	if c, b := httpDo(t, http.MethodGet, base+"/metrics", admin); c != http.StatusOK {
		t.Fatalf("bearer GET /metrics: want 200, got %d: %s", c, b)
	}

	// Security headers on a plain (unauthenticated) response.
	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	for k, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("/healthz header %s = %q, want %q", k, got, want)
		}
	}
}
