package agent

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Default CoreDNS sidecar settings.
const (
	defaultCoreDNSImage = "coredns/coredns:1.11.3"
	dnsContainerName    = "shuttle-dns"
	dnsPortLabel        = "shuttle.dns.port"
	defaultDNSPort      = 53
)

// DNSOptions configures the agent-managed CoreDNS sidecar (private split-horizon
// DNS). The sidecar is started lazily — only when the orchestrator pushes a
// DNSConfigRequest to this host (a dns.yml sidecar provider naming it).
type DNSOptions struct {
	DockerBin string // docker executable (default "docker")
	Image     string // CoreDNS image (default coredns/coredns)
	Container string // container name (default "shuttle-dns")
	Port      int    // host port published for :53 (default 53)
}

func (o DNSOptions) withDefaults() DNSOptions {
	if o.DockerBin == "" {
		o.DockerBin = "docker"
	}
	if o.Image == "" {
		o.Image = defaultCoreDNSImage
	}
	if o.Container == "" {
		o.Container = dnsContainerName
	}
	if o.Port == 0 {
		o.Port = defaultDNSPort
	}
	return o
}

// dnsZone is one zone the sidecar serves.
type dnsZone struct {
	Origin   string
	Zonefile string
}

// dnsSidecar manages a CoreDNS container serving zone files pushed by the
// orchestrator. Config is delivered with `docker cp` of a tar (no shell / bind
// mount needed — works on the distroless CoreDNS image) and CoreDNS's reload +
// file plugins auto-apply changes.
type dnsSidecar struct {
	opts DNSOptions
}

func newDNSSidecar(opts DNSOptions) *dnsSidecar {
	return &dnsSidecar{opts: opts.withDefaults()}
}

func (d *dnsSidecar) docker(ctx context.Context, stdin []byte, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, d.opts.DockerBin, args...)
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

// apply ensures the CoreDNS sidecar is running on the given port and serving the
// given zones. The container is created/recreated when absent or when its port
// changed (`-p` can't change live); otherwise the config tar is copied into the
// running container and CoreDNS reloads on its own.
func (d *dnsSidecar) apply(ctx context.Context, zones []dnsZone, port int) error {
	if port == 0 {
		port = defaultDNSPort
	}
	d.opts.Port = port

	tarball, err := buildCoreDNSTar(zones)
	if err != nil {
		return err
	}

	running := false
	if state, _ := d.docker(ctx, nil, "inspect", "-f", "{{.State.Running}}", d.opts.Container); strings.TrimSpace(state) == "true" {
		running = d.labelPort(ctx) == port
	}
	if running {
		return d.copyConfig(ctx, tarball)
	}

	// (Re)create on the desired port, copy config into the created-but-not-started
	// container so CoreDNS finds it at boot, then start.
	_, _ = d.docker(ctx, nil, "rm", "-f", d.opts.Container)
	if _, err := d.docker(ctx, nil, "create",
		"--name", d.opts.Container,
		"--restart", "unless-stopped",
		"--label", fmt.Sprintf("%s=%d", dnsPortLabel, port),
		"-p", fmt.Sprintf("%d:53/udp", port),
		"-p", fmt.Sprintf("%d:53/tcp", port),
		d.opts.Image, "-conf", "/etc/coredns/Corefile"); err != nil {
		return fmt.Errorf("create coredns sidecar: %w", err)
	}
	if err := d.copyConfig(ctx, tarball); err != nil {
		return err
	}
	if _, err := d.docker(ctx, nil, "start", d.opts.Container); err != nil {
		return fmt.Errorf("start coredns sidecar: %w", err)
	}
	return nil
}

func (d *dnsSidecar) copyConfig(ctx context.Context, tarball []byte) error {
	if _, err := d.docker(ctx, tarball, "cp", "-", d.opts.Container+":/etc/"); err != nil {
		return fmt.Errorf("copy coredns config: %w", err)
	}
	return nil
}

func (d *dnsSidecar) labelPort(ctx context.Context) int {
	out, err := d.docker(ctx, nil, "inspect", "-f", fmt.Sprintf("{{index .Config.Labels %q}}", dnsPortLabel), d.opts.Container)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

// buildCoreDNSTar packs the Corefile + per-zone db files into a tar rooted so
// `docker cp - <c>:/etc/` lands them at /etc/coredns/{Corefile,zones/<origin>.db}.
func buildCoreDNSTar(zones []dnsZone) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, dir := range []string{"coredns/", "coredns/zones/"} {
		if err := tw.WriteHeader(&tar.Header{Name: dir, Mode: 0o755, Typeflag: tar.TypeDir}); err != nil {
			return nil, err
		}
	}
	if err := tarFile(tw, "coredns/Corefile", renderCorefile(zones)); err != nil {
		return nil, err
	}
	for _, z := range zones {
		if err := tarFile(tw, "coredns/zones/"+z.Origin+".db", z.Zonefile); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func tarFile(tw *tar.Writer, name, content string) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := tw.Write([]byte(content))
	return err
}

// renderCorefile builds the CoreDNS Corefile: a server block per zone (served
// from its db file, file-plugin auto-reload), plus a catch-all forwarder. The
// reload plugin re-reads the Corefile so added/removed zones take effect without
// a restart.
func renderCorefile(zones []dnsZone) string {
	var b strings.Builder
	for _, z := range zones {
		fmt.Fprintf(&b, "%s:53 {\n", z.Origin)
		fmt.Fprintf(&b, "    file /etc/coredns/zones/%s.db {\n        reload 15s\n    }\n", z.Origin)
		b.WriteString("    log\n    errors\n}\n")
	}
	b.WriteString(".:53 {\n    forward . 1.1.1.1 9.9.9.9\n    reload 15s\n    log\n    errors\n}\n")
	return b.String()
}
