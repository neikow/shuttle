package agent

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
)

// Default Caddy sidecar ports (HTTP / HTTPS) when a host declares no override.
const (
	defaultCaddyHTTPPort  = 80
	defaultCaddyHTTPSPort = 443
)

// Labels stamped on the sidecar container recording the ports it was created
// with, so reconcileSidecar can detect a port change without parsing port maps.
const (
	caddyHTTPPortLabel  = "shuttle.caddy.http-port"
	caddyHTTPSPortLabel = "shuttle.caddy.https-port"
)

// CaddyOptions configures the agent-managed Caddy sidecar.
type CaddyOptions struct {
	DockerBin string // docker executable (default "docker")
	Image     string // sidecar image (default "caddy:2-alpine")
	Network   string // shared docker network (default "shuttle")
	Container string // sidecar container name (default "shuttle-caddy")
	HTTPPort  int    // host HTTP listen/publish port (default 80)
	HTTPSPort int    // host HTTPS listen/publish port (default 443)
}

func (o CaddyOptions) withDefaults() CaddyOptions {
	if o.DockerBin == "" {
		o.DockerBin = "docker"
	}
	if o.Image == "" {
		o.Image = "caddy:2-alpine"
	}
	if o.Network == "" {
		o.Network = "shuttle"
	}
	if o.Container == "" {
		o.Container = "shuttle-caddy"
	}
	if o.HTTPPort == 0 {
		o.HTTPPort = defaultCaddyHTTPPort
	}
	if o.HTTPSPort == 0 {
		o.HTTPSPort = defaultCaddyHTTPSPort
	}
	return o
}

// caddySidecar manages a Caddy container on a shared docker network. Routed
// service containers join the same network (with a network alias matching the
// service name) so Caddy can reach them by name. Config is pushed with
// `caddy reload` over `docker exec`, so the admin API stays internal to the
// container — no published port or bind mount required.
type caddySidecar struct {
	opts CaddyOptions
}

func newCaddySidecar(opts CaddyOptions) *caddySidecar {
	return &caddySidecar{opts: opts.withDefaults()}
}

func (c *caddySidecar) docker(ctx context.Context, stdin []byte, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.opts.DockerBin, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return out.String(), nil
}

// ensure makes the shared network and a running Caddy sidecar present, listening
// on the host's configured ports. It is idempotent and self-healing: an existing
// network and a running container with matching ports are left alone; a running
// container whose published ports differ (the host's caddy ports changed in the
// repo) is recreated, since `-p` can't change on a live container.
func (c *caddySidecar) ensure(ctx context.Context) error {
	return c.reconcile(ctx, c.opts.HTTPPort, c.opts.HTTPSPort)
}

// reconcile ensures the sidecar is running on the given ports, recreating it
// when the ports differ from the running container's recorded ports.
func (c *caddySidecar) reconcile(ctx context.Context, httpPort, httpsPort int) error {
	if httpPort == 0 {
		httpPort = defaultCaddyHTTPPort
	}
	if httpsPort == 0 {
		httpsPort = defaultCaddyHTTPSPort
	}
	c.opts.HTTPPort, c.opts.HTTPSPort = httpPort, httpsPort

	if _, err := c.docker(ctx, nil, "network", "inspect", c.opts.Network); err != nil {
		if _, err := c.docker(ctx, nil, "network", "create", c.opts.Network); err != nil {
			return fmt.Errorf("create network %s: %w", c.opts.Network, err)
		}
	}
	// A running container on the desired ports is left alone.
	if state, _ := c.docker(ctx, nil, "inspect", "-f", "{{.State.Running}}", c.opts.Container); strings.TrimSpace(state) == "true" {
		gotHTTP := c.labelPort(ctx, caddyHTTPPortLabel)
		gotHTTPS := c.labelPort(ctx, caddyHTTPSPortLabel)
		if gotHTTP == httpPort && gotHTTPS == httpsPort {
			return nil
		}
		slog.Info("caddy sidecar ports changed; recreating",
			"from_http", gotHTTP, "from_https", gotHTTPS, "to_http", httpPort, "to_https", httpsPort)
	}
	// Remove any leftover (stopped, or running on the wrong ports), then start
	// fresh with a blank config (admin on the container's localhost:2019;
	// reachable via docker exec). Ports are stamped as labels so a later
	// reconcile can detect a change without parsing port maps.
	_, _ = c.docker(ctx, nil, "rm", "-f", c.opts.Container)
	_, err := c.docker(ctx, nil, "run", "-d",
		"--name", c.opts.Container,
		"--network", c.opts.Network,
		"--restart", "unless-stopped",
		"--label", fmt.Sprintf("%s=%d", caddyHTTPPortLabel, httpPort),
		"--label", fmt.Sprintf("%s=%d", caddyHTTPSPortLabel, httpsPort),
		"-p", fmt.Sprintf("%d:%d", httpPort, httpPort),
		"-p", fmt.Sprintf("%d:%d", httpsPort, httpsPort),
		c.opts.Image, "caddy", "run")
	if err != nil {
		return fmt.Errorf("run caddy sidecar: %w", err)
	}
	return nil
}

// labelPort reads an integer port label off the running sidecar container,
// returning 0 when absent or unparseable.
func (c *caddySidecar) labelPort(ctx context.Context, label string) int {
	out, err := c.docker(ctx, nil, "inspect", "-f", fmt.Sprintf("{{index .Config.Labels %q}}", label), c.opts.Container)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

// apply pushes a Caddy JSON config to the running sidecar via `caddy reload`.
func (c *caddySidecar) apply(ctx context.Context, configJSON []byte) error {
	if _, err := c.docker(ctx, configJSON,
		"exec", "-i", c.opts.Container, "caddy", "reload", "--config", "/dev/stdin"); err != nil {
		return fmt.Errorf("caddy reload: %w", err)
	}
	return nil
}

// connectProject joins every container of the service's compose project to the
// shared network with alias = service, so Caddy can dial "<service>:<port>".
// Re-connecting an already-attached container is ignored.
func (c *caddySidecar) connectProject(ctx context.Context, composePath, service string) error {
	out, err := c.docker(ctx, nil, "compose", "-f", composePath, "ps", "-q")
	if err != nil {
		return fmt.Errorf("list project containers: %w", err)
	}
	return c.connectContainers(ctx, strings.Fields(out), service)
}

// connectContainers joins the given container IDs to the shared network with
// alias = service. Re-connecting an already-attached container is ignored. Used
// by the rolling-update path to route the new containers before culling the old.
func (c *caddySidecar) connectContainers(ctx context.Context, ids []string, service string) error {
	for _, id := range ids {
		_, err := c.docker(ctx, nil, "network", "connect", "--alias", service, c.opts.Network, id)
		if err != nil && !strings.Contains(err.Error(), "already exists") {
			return fmt.Errorf("connect %s to %s: %w", id[:12], c.opts.Network, err)
		}
	}
	return nil
}
