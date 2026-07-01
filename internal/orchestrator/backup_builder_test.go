package orchestrator

import (
	"context"
	"testing"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/secrets"
)

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "c", "d"); got != "c" {
		t.Errorf("firstNonEmpty = %q, want c", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("firstNonEmpty all-empty = %q, want empty", got)
	}
}

func TestSetBackupConfigAndResolveDefaults(t *testing.T) {
	g := &GitSyncer{}
	g.SetBackupConfig(config.BackupConfig{DefaultStore: "restic", DefaultTarget: "s3:bucket/x"})

	// Service omits store/target -> inherits the bootstrap defaults.
	b := g.resolveBackup(config.Service{Backup: &config.ServiceBackup{Engine: "volume"}})
	if b == nil || b.Store != "restic" || b.Target != "s3:bucket/x" {
		t.Fatalf("resolveBackup = %+v, want restic / s3 default", b)
	}
	// No backup policy -> nil.
	if g.resolveBackup(config.Service{}) != nil {
		t.Error("resolveBackup with no policy should be nil")
	}
	// Store falls back to "local" when neither service nor config set it.
	g2 := &GitSyncer{}
	if b := g2.resolveBackup(config.Service{Backup: &config.ServiceBackup{Engine: "volume"}}); b.Store != config.BackupStoreLocal {
		t.Errorf("store fallback = %q, want %q", b.Store, config.BackupStoreLocal)
	}
}

func TestBackupEnv(t *testing.T) {
	ctx := context.Background()
	fake := secrets.NewFake(map[string]string{"RESTIC_PASSWORD": "hunter2"})
	g := &GitSyncer{
		secrets:   fake,
		backupCfg: config.BackupConfig{Env: []config.BackupCredential{{Key: "RESTIC_PASSWORD", InfisicalKey: "RESTIC_PASSWORD"}}},
	}

	env, err := g.backupEnv(ctx, config.Service{Name: "vol"}, config.BackupEngineVolume)
	if err != nil {
		t.Fatal(err)
	}
	if env["RESTIC_PASSWORD"] != "hunter2" {
		t.Errorf("RESTIC_PASSWORD = %q", env["RESTIC_PASSWORD"])
	}

	// Postgres engine pulls PGPASSWORD from the service's own env (literal here).
	pgEnv, err := g.backupEnv(ctx, config.Service{Name: "db", Env: map[string]string{"PGPASSWORD": "dbpass"}}, config.BackupEnginePostgres)
	if err != nil {
		t.Fatal(err)
	}
	if pgEnv["PGPASSWORD"] != "dbpass" {
		t.Errorf("PGPASSWORD = %q, want dbpass", pgEnv["PGPASSWORD"])
	}

	// Env configured but no secrets provider -> error.
	gNoSec := &GitSyncer{backupCfg: config.BackupConfig{Env: []config.BackupCredential{{Key: "K", InfisicalKey: "K"}}}}
	if _, err := gNoSec.backupEnv(ctx, config.Service{Name: "x"}, config.BackupEngineVolume); err == nil {
		t.Error("backupEnv with creds but no secrets provider should error")
	}
}

func TestBuildBackupRequest(t *testing.T) {
	ctx := context.Background()
	g := &GitSyncer{
		secrets:   secrets.NewFake(nil),
		backupCfg: config.BackupConfig{DefaultTarget: "s3:bucket/x"},
	}
	svc := config.Service{Name: "db", Host: "h1", Backup: &config.ServiceBackup{Engine: "postgres", DBService: "pg", DBUser: "admin"}}
	req, b, err := g.buildBackupRequest(ctx, svc, "bk-1")
	if err != nil {
		t.Fatal(err)
	}
	if req.BackupId != "bk-1" || req.Service != "db" || req.Engine != "postgres" {
		t.Errorf("request = %+v", req)
	}
	if req.Store != config.BackupStoreLocal || req.Target != "s3:bucket/x" {
		t.Errorf("defaults not applied: store=%q target=%q", req.Store, req.Target)
	}
	if req.DbService != "pg" || req.DbUser != "admin" {
		t.Errorf("db fields = %q/%q", req.DbService, req.DbUser)
	}
	_ = b

	// No backup policy -> error.
	if _, _, err := g.buildBackupRequest(ctx, config.Service{Name: "x"}, "id"); err == nil {
		t.Error("buildBackupRequest without a policy should error")
	}

	// No target anywhere -> error.
	gNoTarget := &GitSyncer{secrets: secrets.NewFake(nil)}
	if _, _, err := gNoTarget.buildBackupRequest(ctx, config.Service{Name: "y", Backup: &config.ServiceBackup{Engine: "volume"}}, "id"); err == nil {
		t.Error("buildBackupRequest without a target should error")
	}
}
