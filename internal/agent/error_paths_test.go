package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
)

func TestRollingApply_ScaleFailureAborts(t *testing.T) {
	// pull ok, services listed, but the scale-up `up` fails -> abort, remove new,
	// leave old running.
	d, _ := scriptDriver(t, `case "$args" in
  *"config --services"*) echo web ;;
  *"ps -q web"*) echo old1 ;;
  *" up "*|*"up -d"*) echo "scale failed" >&2; exit 1 ;;
  *) : ;;
esac
exit 0`)
	ch, err := d.Apply(context.Background(), ApplyParams{
		Service: "web", ComposeYAML: []byte("services: {}\n"), WorkDir: t.TempDir(), HealthTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(drainText(ch), "\n"); !strings.Contains(joined, "scale up failed") {
		t.Errorf("expected scale-up abort, got:\n%s", joined)
	}
}

func TestRollingApply_FirstDeployNoOld(t *testing.T) {
	// No existing containers (first deploy): ps returns empty, new container healthy.
	d, _ := scriptDriver(t, `case "$args" in
  *"config --services"*) echo web ;;
  *"ps -q web"*)
     n=$(cat "$CNT" 2>/dev/null || echo 0); n=$((n+1)); echo "$n" > "$CNT"
     if [ "$n" -le 1 ]; then : ; else echo new1; fi ;;
  *inspect*) echo 'running;healthy' ;;
  *) : ;;
esac
exit 0`)
	ch, err := d.Apply(context.Background(), ApplyParams{
		Service: "web", ComposeYAML: []byte("services: {}\n"), WorkDir: t.TempDir(), HealthTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(drainText(ch), "\n"); !strings.Contains(joined, "rolling update complete") {
		t.Errorf("first deploy should complete, got:\n%s", joined)
	}
}

func TestBackup_VolumeNoVolumesFails(t *testing.T) {
	// compose config reports no named volumes -> captureVolumes errors.
	d, _ := scriptDriver(t, `case "$args" in
  *"config --format json"*) printf '{"name":"proj","volumes":{}}' ;;
  *) : ;;
esac
exit 0`)
	logs, done, _ := d.Backup(context.Background(), BackupParams{
		BackupID: "x", Service: "app", Engine: "volume", Store: "local",
		Target: t.TempDir(), WorkDir: backupWorkDir(t),
	})
	if out := runOutcome(t, logs, done); !out.Failed {
		t.Error("volume backup with no named volumes should fail")
	}
}

func TestBackup_ResticParseFailure(t *testing.T) {
	// restic backup emits no parseable summary -> failure.
	d, _ := scriptDriver(t, `case "$args" in
  *"cat config"*) : ;;
  *"config --format json"*) printf '{"name":"proj","volumes":{"data":{"name":"d"}}}' ;;
  *backup*--json*) echo 'no summary here' ;;
  *) : ;;
esac
exit 0`)
	logs, done, _ := d.Backup(context.Background(), BackupParams{
		BackupID: "x", Service: "app", Engine: "volume", Store: "restic",
		Target: "s3:b/x", WorkDir: backupWorkDir(t),
	})
	if out := runOutcome(t, logs, done); !out.Failed {
		t.Error("restic backup with unparseable summary should fail")
	}
}

func TestExecuteTeardown_KeepsWorkspace(t *testing.T) {
	cfg := Config{WorkDir: t.TempDir()}
	dep := newDeployedSet()
	dep.put("web", cfg.WorkDir+"/web", "sha")
	// remove_volumes=false -> workspace kept, service untracked.
	if err := executeTeardown(context.Background(), cfg, &recordSink{}, &fakeDriver{}, dep,
		&shuttlev1.TeardownRequest{Service: "web", RemoveVolumes: false}); err != nil {
		t.Fatal(err)
	}
	if _, ok := dep.snapshot()["web"]; ok {
		t.Error("teardown should untrack the service")
	}
}
