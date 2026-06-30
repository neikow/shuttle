//go:build integration

package integration

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestDNSSidecarResolves drives the private-DNS sidecar end to end against a real
// Docker daemon:
//
//	orchestrator (git sync, short dns_reconcile_interval) + agent → the repo's
//	dns.yml sidecar zone is rendered and pushed → the agent lazily starts a
//	CoreDNS container → querying it resolves the service's domain to the host's
//	configured address.
func TestDNSSidecarResolves(t *testing.T) {
	requireDocker(t)
	dockerPull(t, "coredns/coredns:1.11.3")
	dockerPull(t, "traefik/whoami:latest")
	t.Cleanup(func() { dockerRemoveE2EContainers(t) })
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "docker", "rm", "-f", "shuttle-dns", "shuttle-caddy").Run()
	})

	const (
		host   = "e2e-host"
		bearer = "e2e-bearer"
		zone   = "home.example.com"
		fqdn   = "app.home.example.com"
		wantIP = "100.64.0.5"
	)
	root := repoRoot(t)
	bin := buildBinary(t, root)

	grpcPort := freePort(t)
	httpPort := freePort(t)
	dnsPort := freePort(t)
	httpBase := fmt.Sprintf("http://127.0.0.1:%d", httpPort)

	iac := writeDNSSidecarRepo(t, host, zone, fqdn, wantIP, dnsPort)
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
dns_reconcile_interval: 3s
`, bearer, grpcPort, httpPort, dataDir, iac)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	ctx := t.Context()
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

	// CoreDNS is started lazily once the orchestrator pushes the zone; resolve the
	// service domain against it (UDP on the published port) until it returns the
	// host's configured address.
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp", fmt.Sprintf("127.0.0.1:%d", dnsPort))
		},
	}
	waitFor(t, 120*time.Second, "CoreDNS sidecar to resolve "+fqdn, func() bool {
		qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		addrs, err := resolver.LookupHost(qctx, fqdn)
		if err != nil {
			return false
		}
		for _, a := range addrs {
			if a == wantIP {
				return true
			}
		}
		t.Logf("resolved %s -> %v (want %s)", fqdn, addrs, wantIP)
		return false
	})
}

// writeDNSSidecarRepo scaffolds an IaC repo whose dns.yml routes a private zone
// to a CoreDNS sidecar on the agent host, with one service whose domain falls
// under that zone and points at the host's "tailscale" address.
func writeDNSSidecarRepo(t *testing.T, host, zone, fqdn, ip string, dnsPort int) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "hosts.yaml"), fmt.Sprintf(
		"hosts:\n  - name: %s\n    addresses:\n      tailscale: %s\n", host, ip))
	mustWrite(t, filepath.Join(dir, "dns.yml"), fmt.Sprintf(
		"providers:\n  - name: home\n    type: sidecar\n    host: %s\n    port: %d\n"+
			"zones:\n  - domain: %s\n    provider: home\n    address: tailscale\n",
		host, dnsPort, zone))

	svcDir := filepath.Join(dir, "services", "app")
	mustWrite(t, filepath.Join(svcDir, "app.yaml"), fmt.Sprintf(
		"name: app\nhost: %s\nupdate_policy: recreate\ndomains: [%s]\n", host, fqdn))
	mustWrite(t, filepath.Join(svcDir, "docker-compose.yml"), `services:
  app:
    image: traefik/whoami:latest
    labels:
      - "shuttle-e2e=1"
    restart: "no"
`)
	gitInit(t, dir)
	return dir
}
