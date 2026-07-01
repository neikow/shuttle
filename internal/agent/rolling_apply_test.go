package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

// rollingScript drives a full rolling update: list one service "web", report one
// old container before the scale-up and old+new after it, and report the new
// container healthy. The $CNT counter distinguishes the two `ps -q web` calls.
const rollingScript = `
case "$args" in
  *"config --services"*) echo web ;;
  *"ps -q web"*)
     n=$(cat "$CNT" 2>/dev/null || echo 0); n=$((n+1)); echo "$n" > "$CNT"
     if [ "$n" -le 1 ]; then echo old1; else printf 'old1\nnew1\n'; fi ;;
  *inspect*) echo 'running;healthy' ;;
  *) : ;;
esac
exit 0`

func TestRollingApply_Success(t *testing.T) {
	d, _ := scriptDriver(t, rollingScript)
	work := t.TempDir()
	var connected []string
	ch, err := d.Apply(context.Background(), ApplyParams{
		Service:       "web",
		ComposeYAML:   []byte("services:\n  web:\n    image: nginx\n"),
		WorkDir:       work,
		HealthTimeout: 10 * time.Second,
		OnNewContainers: func(_ context.Context, ids []string) error {
			connected = append(connected, ids...)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := drainText(ch)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "rolling update complete") {
		t.Fatalf("expected completion line, got:\n%s", joined)
	}
	if strings.Contains(joined, "compose error") {
		t.Fatalf("unexpected error in rolling update:\n%s", joined)
	}
	if len(connected) != 1 || connected[0] != "new1" {
		t.Errorf("OnNewContainers got %v, want [new1]", connected)
	}
}

func TestRollingApply_PullFailureAborts(t *testing.T) {
	// pull exits non-zero -> abort before touching containers.
	d, _ := scriptDriver(t, `case "$args" in *pull*) echo "no such image" >&2; exit 1 ;; *) : ;; esac
exit 0`)
	ch, err := d.Apply(context.Background(), ApplyParams{
		Service: "web", ComposeYAML: []byte("services: {}\n"), WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(drainText(ch), "\n"); !strings.Contains(joined, "pull failed") {
		t.Errorf("expected pull failure abort, got:\n%s", joined)
	}
}

func TestContainerReady(t *testing.T) {
	cases := []struct {
		name, inspect string
		wantReady     bool
		wantErr       bool
	}{
		{"healthy", "running;healthy", true, false},
		{"unhealthy", "running;unhealthy", false, true},
		{"running no healthcheck", "running;none", true, false},
		{"created no healthcheck", "created;none", false, false},
		{"starting", "running;starting", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, _ := scriptDriver(t, "printf '%s' '"+c.inspect+"'\nexit 0")
			ready, err := d.containerReady(context.Background(), "cid")
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if ready != c.wantReady {
				t.Errorf("ready = %v, want %v", ready, c.wantReady)
			}
		})
	}
}

func TestWaitHealthy_NoContainers(t *testing.T) {
	d := NewComposeDriver()
	if err := d.waitHealthy(context.Background(), nil, time.Second); err != nil {
		t.Errorf("waitHealthy(nil) = %v, want nil", err)
	}
}
