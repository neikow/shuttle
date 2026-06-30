package agent

import (
	"context"
	"testing"
)

func TestCaddyReconcile_StartsWhenAbsent(t *testing.T) {
	// Everything succeeds with empty output: network exists, no running container
	// -> rm + run a fresh sidecar.
	c := newCaddySidecar(CaddyOptions{DockerBin: stubBin(t, "exit 0")})
	if err := c.ensure(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
}

func TestCaddyReconcile_NoopWhenRunningSamePorts(t *testing.T) {
	// Running container on the requested ports -> left alone (no error).
	bin := stubBin(t, `case "$args" in
  *"State.Running"*) echo true ;;
  *"http-port"*) echo 80 ;;
  *"https-port"*) echo 443 ;;
  *) : ;;
esac
exit 0`)
	c := newCaddySidecar(CaddyOptions{DockerBin: bin})
	if err := c.reconcile(context.Background(), 80, 443); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

func TestCaddyApply(t *testing.T) {
	c := newCaddySidecar(CaddyOptions{DockerBin: stubBin(t, "exit 0")})
	if err := c.apply(context.Background(), []byte(`{"x":1}`)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	cFail := newCaddySidecar(CaddyOptions{DockerBin: stubBin(t, "echo boom >&2; exit 1")})
	if err := cFail.apply(context.Background(), []byte(`{}`)); err == nil {
		t.Error("apply should surface a reload failure")
	}
}

func TestCaddyConnectProject(t *testing.T) {
	bin := stubBin(t, `case "$args" in *"ps -q"*) printf 'id1\nid2\n' ;; *) : ;; esac
exit 0`)
	c := newCaddySidecar(CaddyOptions{DockerBin: bin})
	if err := c.connectProject(context.Background(), "/work/docker-compose.yml", "web"); err != nil {
		t.Fatalf("connectProject: %v", err)
	}
}

func TestCaddyConnectContainers_IgnoresAlreadyConnected(t *testing.T) {
	// network connect failing with "already exists" is tolerated.
	bin := stubBin(t, `echo "Error response: endpoint already exists" >&2; exit 1`)
	c := newCaddySidecar(CaddyOptions{DockerBin: bin})
	if err := c.connectContainers(context.Background(), []string{"id1"}, "web"); err != nil {
		t.Errorf("connectContainers should ignore already-exists: %v", err)
	}
}

func TestCoreDNSApply_CreatesWhenAbsent(t *testing.T) {
	d := newDNSSidecar(DNSOptions{DockerBin: stubBin(t, "exit 0")})
	if err := d.apply(context.Background(), []dnsZone{{Origin: "home.example.com", Zonefile: "zone"}}, 5353); err != nil {
		t.Fatalf("apply: %v", err)
	}
}

func TestCoreDNSApply_UpdatesWhenRunningSamePort(t *testing.T) {
	bin := stubBin(t, `case "$args" in
  *"State.Running"*) echo true ;;
  *"Config.Labels"*) echo 5353 ;;
  *) : ;;
esac
exit 0`)
	d := newDNSSidecar(DNSOptions{DockerBin: bin})
	if err := d.apply(context.Background(), []dnsZone{{Origin: "home.example.com", Zonefile: "zone"}}, 5353); err != nil {
		t.Fatalf("apply (running, same port): %v", err)
	}
}

func TestCoreDNSApply_CreateFailure(t *testing.T) {
	// `create` fails -> apply errors. Only the create call (4th+ token) fails.
	bin := stubBin(t, `case "$args" in *create*) echo boom >&2; exit 1 ;; *) : ;; esac
exit 0`)
	d := newDNSSidecar(DNSOptions{DockerBin: bin})
	if err := d.apply(context.Background(), []dnsZone{{Origin: "z", Zonefile: "x"}}, 53); err == nil {
		t.Error("apply should fail when create fails")
	}
}
