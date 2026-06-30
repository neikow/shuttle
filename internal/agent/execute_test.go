package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
)

// fakeDriver implements Driver with canned channels for testing the client's
// execute* orchestration without Docker.
type fakeDriver struct {
	applyErr  error
	backupOut BackupOutcome
	calls     []string
}

func (f *fakeDriver) Apply(_ context.Context, _ ApplyParams) (<-chan LogLine, error) {
	f.calls = append(f.calls, "apply")
	if f.applyErr != nil {
		return nil, f.applyErr
	}
	return feed([]LogLine{{Stream: "stdout", Text: "ok"}}), nil
}

func (f *fakeDriver) Rollback(_ context.Context, _ RollbackParams) (<-chan LogLine, error) {
	f.calls = append(f.calls, "rollback")
	return feed([]LogLine{{Stream: "stdout", Text: "ok"}}), nil
}

func (f *fakeDriver) Status(_ context.Context, _, _ string) (string, error) {
	return "running", nil
}

func (f *fakeDriver) Down(_ context.Context, _, _ string, _ bool) (<-chan LogLine, error) {
	f.calls = append(f.calls, "down")
	return feed(nil), nil
}

func (f *fakeDriver) Backup(_ context.Context, _ BackupParams) (<-chan LogLine, <-chan BackupOutcome, error) {
	f.calls = append(f.calls, "backup")
	done := make(chan BackupOutcome, 1)
	done <- f.backupOut
	close(done)
	return feed(nil), done, nil
}

func (f *fakeDriver) Restore(_ context.Context, _ RestoreParams) (<-chan LogLine, <-chan BackupOutcome, error) {
	f.calls = append(f.calls, "restore")
	done := make(chan BackupOutcome, 1)
	done <- f.backupOut
	close(done)
	return feed(nil), done, nil
}

func (r *recordSink) backupResult() *shuttlev1.BackupResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range r.events {
		if br, ok := ev.Payload.(*shuttlev1.AgentEvent_BackupResult); ok {
			return br.BackupResult
		}
	}
	return nil
}

func okCaddy(t *testing.T) *caddySidecar {
	return newCaddySidecar(CaddyOptions{DockerBin: stubBin(t, "exit 0")})
}

func TestExecuteDeploy(t *testing.T) {
	cfg := Config{WorkDir: t.TempDir()}
	dep := newDeployedSet()
	sink := &recordSink{}
	drv := &fakeDriver{}
	err := executeDeploy(context.Background(), cfg, sink, drv, dep, okCaddy(t),
		&shuttlev1.DeployRequest{DeployId: "d1", Service: "web", Sha: "abc", ComposeYaml: []byte("services: {}\n")})
	if err != nil {
		t.Fatalf("executeDeploy: %v", err)
	}
	if res := sink.result(); res == nil || res.Status != shuttlev1.DeployStatus_DEPLOY_STATUS_SUCCESS {
		t.Fatalf("result = %+v, want SUCCESS", res)
	}
	if _, ok := dep.snapshot()["web"]; !ok {
		t.Error("deployed set should track web after a successful deploy")
	}
}

func TestExecuteRollback(t *testing.T) {
	cfg := Config{WorkDir: t.TempDir()}
	sink := &recordSink{}
	err := executeRollback(context.Background(), cfg, sink, &fakeDriver{}, newDeployedSet(), okCaddy(t),
		&shuttlev1.RollbackRequest{DeployId: "r1", Service: "web", TargetSha: "old"})
	if err != nil {
		t.Fatalf("executeRollback: %v", err)
	}
	if res := sink.result(); res == nil || res.Status != shuttlev1.DeployStatus_DEPLOY_STATUS_SUCCESS {
		t.Errorf("result = %+v, want SUCCESS", res)
	}
}

func TestExecuteTeardown_RemovesWorkspace(t *testing.T) {
	cfg := Config{WorkDir: t.TempDir()}
	work := filepath.Join(cfg.WorkDir, "web")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	dep := newDeployedSet()
	dep.put("web", work, "sha")
	err := executeTeardown(context.Background(), cfg, &recordSink{}, &fakeDriver{}, dep,
		&shuttlev1.TeardownRequest{Service: "web", RemoveVolumes: true})
	if err != nil {
		t.Fatalf("executeTeardown: %v", err)
	}
	if _, ok := dep.snapshot()["web"]; ok {
		t.Error("teardown should stop tracking the service")
	}
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Error("remove_volumes teardown should delete the workspace dir")
	}
}

func TestExecuteBackupAndRestore(t *testing.T) {
	cfg := Config{WorkDir: t.TempDir()}
	drv := &fakeDriver{backupOut: BackupOutcome{SnapshotID: "snap1", SizeBytes: 42}}

	sink := &recordSink{}
	if err := executeBackup(context.Background(), cfg, sink, drv,
		&shuttlev1.BackupRequest{BackupId: "b1", Service: "db", Engine: "volume", Store: "local"}); err != nil {
		t.Fatalf("executeBackup: %v", err)
	}
	if br := sink.backupResult(); br == nil || br.SnapshotId != "snap1" || br.SizeBytes != 42 {
		t.Errorf("backup result = %+v, want snap1/42", br)
	}

	sink2 := &recordSink{}
	if err := executeRestore(context.Background(), cfg, sink2, drv,
		&shuttlev1.RestoreRequest{OperationId: "op1", Service: "db", Engine: "volume", Store: "local", SnapshotId: "snap1"}); err != nil {
		t.Fatalf("executeRestore: %v", err)
	}
	if br := sink2.backupResult(); br == nil || br.Operation != "restore" {
		t.Errorf("restore result = %+v, want operation=restore", br)
	}
}

func TestHandleCommand_Dispatches(t *testing.T) {
	cfg := Config{WorkDir: t.TempDir()}
	drv := &fakeDriver{}
	caddy := okCaddy(t)
	dns := newDNSSidecar(DNSOptions{DockerBin: stubBin(t, "exit 0")})
	dep := newDeployedSet()

	cmds := []*shuttlev1.OrchestratorCommand{
		{Payload: &shuttlev1.OrchestratorCommand_Deploy{Deploy: &shuttlev1.DeployRequest{DeployId: "d", Service: "web", ComposeYaml: []byte("services: {}\n")}}},
		{Payload: &shuttlev1.OrchestratorCommand_Rollback{Rollback: &shuttlev1.RollbackRequest{DeployId: "r", Service: "web"}}},
		{Payload: &shuttlev1.OrchestratorCommand_Teardown{Teardown: &shuttlev1.TeardownRequest{Service: "web"}}},
		{Payload: &shuttlev1.OrchestratorCommand_Backup{Backup: &shuttlev1.BackupRequest{BackupId: "b", Service: "db"}}},
		{Payload: &shuttlev1.OrchestratorCommand_Restore{Restore: &shuttlev1.RestoreRequest{OperationId: "o", Service: "db"}}},
	}
	for _, c := range cmds {
		if err := handleCommand(context.Background(), cfg, &recordSink{}, drv, dep, caddy, dns, c); err != nil {
			t.Errorf("handleCommand %T: %v", c.Payload, err)
		}
	}
	want := []string{"apply", "rollback", "down", "backup", "restore"}
	if len(drv.calls) != len(want) {
		t.Fatalf("driver calls = %v, want %v", drv.calls, want)
	}
	for i, w := range want {
		if drv.calls[i] != w {
			t.Errorf("call[%d] = %q, want %q", i, drv.calls[i], w)
		}
	}
}

func TestTokenCreds(t *testing.T) {
	c := tokenCreds{token: "abc"}
	md, err := c.GetRequestMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if md["authorization"] != "Bearer abc" {
		t.Errorf("metadata = %v, want Bearer abc", md)
	}
	if c.RequireTransportSecurity() {
		t.Error("RequireTransportSecurity should be false (works over insecure dev channel)")
	}
}
