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

// TestOverviewReportsAgentVersion verifies a connected agent's build version
// flows through register → registry → GET /overview, so an operator can see
// which version each host is running (the basis for skew detection). No
// containers are deployed, so this needs only an agent connection.
func TestOverviewReportsAgentVersion(t *testing.T) {
	root := repoRoot(t)
	bin := buildBinary(t, root)

	grpcPort := freePort(t)
	httpPort := freePort(t)
	httpBase := fmt.Sprintf("http://127.0.0.1:%d", httpPort)
	const (
		host   = "ver-host"
		bearer = "e2e-bearer"
	)

	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	writeFileOrFail(t, cfgPath, fmt.Sprintf(`bearer_token: %s
grpc_addr: "127.0.0.1:%d"
http_addr: "127.0.0.1:%d"
data_dir: %s
`, bearer, grpcPort, httpPort, t.TempDir()))

	ctx := t.Context()
	startProc(ctx, t, "orchestrator", bin, "orchestrator", "--config", cfgPath)
	waitFor(t, 30*time.Second, "orchestrator /healthz", func() bool {
		code, _ := httpDo(t, http.MethodGet, httpBase+"/healthz", "")
		return code == http.StatusOK
	})

	startProc(ctx, t, "agent", bin, "agent",
		"--orchestrator", fmt.Sprintf("127.0.0.1:%d", grpcPort),
		"--host", host, "--work-dir", t.TempDir(),
	)

	// The default (un-stamped) build version is "dev"; assert the connected
	// host surfaces a non-empty agent_version through /overview.
	waitFor(t, 30*time.Second, "host to appear in /overview with a version", func() bool {
		code, body := httpDo(t, http.MethodGet, httpBase+"/overview", bearer)
		if code != http.StatusOK {
			return false
		}
		var ov struct {
			Hosts []struct {
				Name         string `json:"name"`
				Connected    bool   `json:"connected"`
				AgentVersion string `json:"agent_version"`
			} `json:"hosts"`
		}
		if err := json.Unmarshal([]byte(body), &ov); err != nil {
			return false
		}
		for _, h := range ov.Hosts {
			if h.Name == host && h.Connected && h.AgentVersion != "" {
				return true
			}
		}
		return false
	})
}
