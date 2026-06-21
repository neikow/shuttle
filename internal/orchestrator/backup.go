package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/ledger"
	"github.com/neikow/shuttle/internal/secrets"
)

// preDeployBackupCooldown bounds how often a before_deploy snapshot fires, so a
// crash-looping service whose drift heal redeploys every tick does not spawn a
// backup every tick. A real release still snapshots; a repeated redeploy within
// the window reuses the recent backup as its restore point.
const preDeployBackupCooldown = 5 * time.Minute

// SetBackupConfig attaches the bootstrap backup configuration (backend
// credentials + store/target defaults). Call before serving.
func (g *GitSyncer) SetBackupConfig(c config.BackupConfig) { g.backupCfg = c }

// resolveBackup returns the service's backup policy with bootstrap defaults
// applied (store/target inherited from BackupConfig when the service omits
// them), or nil when the service declares no backup.
func (g *GitSyncer) resolveBackup(svc config.Service) *config.ServiceBackup {
	if svc.Backup == nil {
		return nil
	}
	b := *svc.Backup // copy so defaults don't mutate the loaded repo
	if b.Store == "" {
		b.Store = g.backupCfg.DefaultStore
	}
	if b.Store == "" {
		b.Store = config.BackupStoreLocal
	}
	if b.Target == "" {
		b.Target = g.backupCfg.DefaultTarget
	}
	return &b
}

// backupEnv resolves the environment injected into the agent's backup/restore
// process: the configured backend credentials (RESTIC_PASSWORD, AWS_*, ...) plus,
// for the postgres engine, PGPASSWORD pulled from the service's own secrets. The
// values are resolved fresh per operation and never persisted.
func (g *GitSyncer) backupEnv(ctx context.Context, svc config.Service, engine string) (map[string]string, error) {
	env := map[string]string{}
	if len(g.backupCfg.Env) > 0 && g.secrets == nil {
		return nil, fmt.Errorf("backups.env configured but no secrets provider")
	}
	for _, c := range g.backupCfg.Env {
		v, err := g.secrets.Get(ctx, secrets.Scope{Env: c.InfisicalEnv, Path: c.InfisicalPath}, c.InfisicalKey)
		if err != nil {
			return nil, fmt.Errorf("backup credential %q: %w", c.Key, err)
		}
		env[c.Key] = v
	}
	if engine == config.BackupEnginePostgres && g.secrets != nil {
		all, err := g.renderEnv(ctx, svc)
		if err != nil {
			// Don't fail the backup on env_schema resolution; the DB may use
			// peer/trust auth. Log and proceed without a password.
			slog.Warn("backup: could not resolve service secrets for PGPASSWORD", "service", svc.Name, "err", err)
		} else if pw := firstNonEmpty(all["PGPASSWORD"], all["POSTGRES_PASSWORD"]); pw != "" {
			env["PGPASSWORD"] = pw
		}
	}
	return env, nil
}

// buildBackupRequest assembles the BackupRequest for a service, resolving store/
// target defaults, backend credentials, and retention. Returns the request and
// the host to dispatch it to.
func (g *GitSyncer) buildBackupRequest(ctx context.Context, svc config.Service, backupID string) (*shuttlev1.BackupRequest, *config.ServiceBackup, error) {
	b := g.resolveBackup(svc)
	if b == nil {
		return nil, nil, fmt.Errorf("service %q declares no backup policy", svc.Name)
	}
	if b.Target == "" {
		return nil, nil, fmt.Errorf("service %q backup has no target (set backup.target or backups.default_target)", svc.Name)
	}
	env, err := g.backupEnv(ctx, svc, b.Engine)
	if err != nil {
		return nil, nil, err
	}
	return &shuttlev1.BackupRequest{
		BackupId:  backupID,
		Service:   svc.Name,
		Engine:    b.Engine,
		Store:     b.Store,
		Target:    b.Target,
		Env:       env,
		Volumes:   b.Volumes,
		DbService: b.DBService,
		DbUser:    b.DBUser,
		DbName:    b.DBName,
		Retention: retentionToProto(b.Retention),
	}, b, nil
}

// BackupService dispatches a backup of one service and records a pending row.
// Returns the backup id and the host it was sent to.
func (g *GitSyncer) BackupService(ctx context.Context, service string, trigger ledger.TriggeredBy) (backupID, host string, err error) {
	svc, err := g.serviceFromWorkingRepo(ctx, service)
	if err != nil {
		return "", "", err
	}
	backupID = newID()
	req, b, err := g.buildBackupRequest(ctx, *svc, backupID)
	if err != nil {
		return "", "", err
	}
	rec := ledger.BackupRecord{
		BackupID: backupID, Service: svc.Name, Host: svc.Host,
		Engine: b.Engine, Store: b.Store, Target: b.Target,
		Status: ledger.StatusPending, TriggeredBy: trigger, StartedAt: time.Now(),
	}
	if err := g.store.RecordBackup(ctx, rec); err != nil {
		return "", "", fmt.Errorf("record backup: %w", err)
	}
	cmd := &shuttlev1.OrchestratorCommand{Payload: &shuttlev1.OrchestratorCommand_Backup{Backup: req}}
	if err := g.registry.Send(svc.Host, cmd); err != nil {
		_ = g.store.MarkBackupResult(ctx, backupID, ledger.StatusFailed, "", 0, "send to agent failed")
		g.bus.Publish(Event{Type: EventBackupFailed, Service: svc.Name, Host: svc.Host, DeployID: backupID,
			Status: string(ledger.StatusFailed), Message: "send to agent failed"})
		return "", "", fmt.Errorf("send backup to agent: %w", err)
	}
	slog.Info("backup dispatched", "backup_id", backupID, "service", svc.Name, "host", svc.Host, "engine", b.Engine, "store", b.Store)
	g.bus.Publish(Event{Type: EventBackupQueued, Service: svc.Name, Host: svc.Host, DeployID: backupID,
		Status: string(ledger.StatusPending), Detail: map[string]string{"triggered_by": string(trigger)}})
	return backupID, svc.Host, nil
}

// RestoreService dispatches a restore of a service from a prior backup. When
// backupID is empty the most recent successful backup is used. The restore
// inherits the store/target/snapshot of the chosen backup, so it always reads
// from where that backup was written.
func (g *GitSyncer) RestoreService(ctx context.Context, service, backupID string) (operationID, host, snapshotID string, err error) {
	svc, err := g.serviceFromWorkingRepo(ctx, service)
	if err != nil {
		return "", "", "", err
	}
	b := g.resolveBackup(*svc)
	if b == nil {
		return "", "", "", fmt.Errorf("service %q declares no backup policy", service)
	}
	var rec ledger.BackupRecord
	var ok bool
	if backupID == "" {
		rec, ok, err = g.store.LatestSuccessfulBackup(ctx, service)
		if err == nil && !ok {
			err = fmt.Errorf("service %q has no successful backup to restore", service)
		}
	} else {
		rec, ok, err = g.store.BackupByID(ctx, backupID)
		switch {
		case err == nil && !ok:
			err = fmt.Errorf("backup %q not found", backupID)
		case err == nil && rec.Service != service:
			err = fmt.Errorf("backup %q belongs to service %q, not %q", backupID, rec.Service, service)
		case err == nil && rec.Status != ledger.StatusSuccess:
			err = fmt.Errorf("backup %q did not succeed (status %s)", backupID, rec.Status)
		}
	}
	if err != nil {
		return "", "", "", err
	}

	env, err := g.backupEnv(ctx, *svc, rec.Engine)
	if err != nil {
		return "", "", "", err
	}
	operationID = newID()
	req := &shuttlev1.RestoreRequest{
		OperationId: operationID,
		Service:     service,
		Engine:      rec.Engine,
		Store:       rec.Store,
		Target:      rec.Target,
		SnapshotId:  rec.SnapshotID,
		Env:         env,
		DbService:   b.DBService,
		DbUser:      b.DBUser,
		DbName:      b.DBName,
	}
	cmd := &shuttlev1.OrchestratorCommand{Payload: &shuttlev1.OrchestratorCommand_Restore{Restore: req}}
	if err := g.registry.Send(svc.Host, cmd); err != nil {
		g.bus.Publish(Event{Type: EventRestoreFailed, Service: service, Host: svc.Host, DeployID: operationID,
			Message: "send to agent failed"})
		return "", "", "", fmt.Errorf("send restore to agent: %w", err)
	}
	slog.Info("restore dispatched", "operation_id", operationID, "service", service, "host", svc.Host, "snapshot", rec.SnapshotID)
	return operationID, svc.Host, rec.SnapshotID, nil
}

// maybeBackupBeforeDeploy dispatches a best-effort snapshot before a deploy of a
// service whose policy sets before_deploy. Because an agent processes stream
// commands sequentially, enqueueing the backup before the deploy makes the agent
// finish the snapshot before the deploy runs — ordering, not a synchronous
// barrier. A failure (or a backup within the cooldown) never blocks the deploy.
func (g *GitSyncer) maybeBackupBeforeDeploy(ctx context.Context, svc config.Service) {
	b := g.resolveBackup(svc)
	if b == nil || !b.BeforeDeploy {
		return
	}
	if last, ok, err := g.store.LatestBackupStart(ctx, svc.Name); err == nil && ok && time.Since(last) < preDeployBackupCooldown {
		slog.Debug("skip pre-deploy backup (within cooldown)", "service", svc.Name)
		return
	}
	if _, _, err := g.BackupService(ctx, svc.Name, ledger.TriggeredByPreDeploy); err != nil {
		slog.Warn("pre-deploy backup failed (continuing with deploy)", "service", svc.Name, "err", err)
	}
}

// serviceFromWorkingRepo loads the service from the synced working copy, syncing
// once if the repo is not present yet. Backups read the already-synced working
// tree (kept fresh by the drift reconciler) rather than git-pulling per backup.
func (g *GitSyncer) serviceFromWorkingRepo(ctx context.Context, service string) (*config.Service, error) {
	repo, err := config.Load(g.dir)
	if err != nil {
		if _, serr := g.Sync(ctx); serr != nil {
			return nil, serr
		}
		repo, err = config.Load(g.dir)
		if err != nil {
			return nil, err
		}
	}
	for i := range repo.Services {
		if repo.Services[i].Name == service {
			return &repo.Services[i], nil
		}
	}
	return nil, fmt.Errorf("service %q not found", service)
}

func retentionToProto(r config.BackupRetention) *shuttlev1.BackupRetention {
	return &shuttlev1.BackupRetention{
		KeepLast:    int32(r.KeepLast),
		KeepDaily:   int32(r.KeepDaily),
		KeepWeekly:  int32(r.KeepWeekly),
		KeepMonthly: int32(r.KeepMonthly),
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// BackupScheduler periodically dispatches scheduled backups: every tick it loads
// the synced repo and, for each service whose backup.schedule is due (now minus
// its last backup ≥ its interval), dispatches a backup. It is the backup analogue
// of the drift reconciler, and like it reads the working copy the reconciler
// keeps synced rather than pulling git itself.
type BackupScheduler struct {
	syncer   *GitSyncer
	store    *ledger.Store
	interval time.Duration
}

// NewBackupScheduler builds a scheduler ticking every interval (the poll cadence;
// the per-service schedule decides whether a service is actually due).
func NewBackupScheduler(syncer *GitSyncer, store *ledger.Store, interval time.Duration) *BackupScheduler {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	return &BackupScheduler{syncer: syncer, store: store, interval: interval}
}

// Run ticks until ctx is cancelled.
func (s *BackupScheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *BackupScheduler) tick(ctx context.Context) {
	repo, err := config.Load(s.syncer.LocalDir())
	if err != nil {
		slog.Error("backup scheduler: load repo failed", "err", err)
		return
	}
	for _, svc := range repo.Services {
		if svc.Backup == nil {
			continue
		}
		interval, err := svc.Backup.ScheduleInterval()
		if err != nil || interval <= 0 {
			continue // no schedule (or invalid; validated at load, so unreachable)
		}
		last, ok, err := s.store.LatestBackupStart(ctx, svc.Name)
		if err != nil {
			slog.Error("backup scheduler: latest backup lookup failed", "service", svc.Name, "err", err)
			continue
		}
		if ok && time.Since(last) < interval {
			continue // not due yet
		}
		if _, _, err := s.syncer.BackupService(ctx, svc.Name, ledger.TriggeredBySchedule); err != nil {
			slog.Error("scheduled backup failed", "service", svc.Name, "err", err)
		}
	}
}
