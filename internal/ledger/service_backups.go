package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Backup trigger sources, beyond the deploy TriggeredBy* values.
const (
	TriggeredBySchedule  TriggeredBy = "schedule"   // the periodic backup scheduler
	TriggeredByPreDeploy TriggeredBy = "pre_deploy" // best-effort snapshot before a deploy
)

// BackupRecord is one row of the service_backups table: a single backup attempt.
type BackupRecord struct {
	BackupID    string      `json:"backup_id"`
	Service     string      `json:"service"`
	Host        string      `json:"host"`
	Engine      string      `json:"engine"`
	Store       string      `json:"store"`
	Target      string      `json:"target,omitempty"`
	SnapshotID  string      `json:"snapshot_id,omitempty"`
	SizeBytes   int64       `json:"size_bytes"`
	Status      Status      `json:"status"`
	TriggeredBy TriggeredBy `json:"triggered_by"`
	Error       string      `json:"error,omitempty"`
	StartedAt   time.Time   `json:"started_at"`
	FinishedAt  *time.Time  `json:"finished_at,omitempty"`
}

// RecordBackup inserts a backup attempt in the pending state. Called when the
// orchestrator dispatches the backup to the agent.
func (s *Store) RecordBackup(ctx context.Context, r BackupRecord) error {
	if r.Status == "" {
		r.Status = StatusPending
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO service_backups
		   (backup_id, service, host, engine, store, target, status, triggered_by, started_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.BackupID, r.Service, r.Host, r.Engine, r.Store, r.Target,
		string(r.Status), string(r.TriggeredBy), r.StartedAt.UnixMilli(),
	)
	return err
}

// MarkBackupResult finalizes a backup attempt with the agent's reported outcome.
// snapshotID and sizeBytes are recorded on success; errMsg on failure. A
// terminal status stamps finished_at.
func (s *Store) MarkBackupResult(ctx context.Context, backupID string, status Status, snapshotID string, sizeBytes int64, errMsg string) error {
	var finishedAt *int64
	if status == StatusSuccess || status == StatusFailed {
		t := time.Now().UnixMilli()
		finishedAt = &t
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE service_backups
		   SET status = ?, snapshot_id = ?, size_bytes = ?, error = ?, finished_at = ?
		 WHERE backup_id = ?`,
		string(status), snapshotID, sizeBytes, errMsg, finishedAt, backupID,
	)
	return err
}

// ListBackups returns the most recent backup attempts for a service (all
// services when service is ""), newest first, capped at limit.
func (s *Store) ListBackups(ctx context.Context, service string, limit int) ([]BackupRecord, error) {
	var (
		rows *sql.Rows
		err  error
	)
	const cols = `backup_id, service, host, engine, store, target, snapshot_id,
	              size_bytes, status, triggered_by, error, started_at, finished_at`
	if service == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+cols+` FROM service_backups ORDER BY started_at DESC LIMIT ?`, limit)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+cols+` FROM service_backups WHERE service = ? ORDER BY started_at DESC LIMIT ?`,
			service, limit)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []BackupRecord
	for rows.Next() {
		r, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// BackupByID returns one backup attempt. The bool is false when no such id
// exists.
func (s *Store) BackupByID(ctx context.Context, backupID string) (BackupRecord, bool, error) {
	const cols = `backup_id, service, host, engine, store, target, snapshot_id,
	              size_bytes, status, triggered_by, error, started_at, finished_at`
	r, err := scanBackup(s.db.QueryRowContext(ctx,
		`SELECT `+cols+` FROM service_backups WHERE backup_id = ?`, backupID))
	if errors.Is(err, sql.ErrNoRows) {
		return BackupRecord{}, false, nil
	}
	if err != nil {
		return BackupRecord{}, false, err
	}
	return r, true, nil
}

// LatestBackupStart returns when a service's most recent backup attempt started.
// The bool is false when the service has no backup yet. Used by the scheduler to
// decide whether a service's next scheduled backup is due.
func (s *Store) LatestBackupStart(ctx context.Context, service string) (time.Time, bool, error) {
	var ms int64
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(started_at) FROM service_backups WHERE service = ?`, service).Scan(&ms)
	if errors.Is(err, sql.ErrNoRows) || ms == 0 {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return time.UnixMilli(ms), true, nil
}

// LatestSuccessfulBackup returns a service's most recent successful backup,
// the natural default target for a restore. The bool is false when none exists.
func (s *Store) LatestSuccessfulBackup(ctx context.Context, service string) (BackupRecord, bool, error) {
	const cols = `backup_id, service, host, engine, store, target, snapshot_id,
	              size_bytes, status, triggered_by, error, started_at, finished_at`
	r, err := scanBackup(s.db.QueryRowContext(ctx,
		`SELECT `+cols+` FROM service_backups
		 WHERE service = ? AND status = 'success'
		 ORDER BY started_at DESC LIMIT 1`, service))
	if errors.Is(err, sql.ErrNoRows) {
		return BackupRecord{}, false, nil
	}
	if err != nil {
		return BackupRecord{}, false, err
	}
	return r, true, nil
}

// scanner abstracts *sql.Row and *sql.Rows so scanBackup serves both.
type scanner interface{ Scan(dest ...any) error }

func scanBackup(sc scanner) (BackupRecord, error) {
	var (
		r          BackupRecord
		startedMs  int64
		finishedMs sql.NullInt64
	)
	if err := sc.Scan(
		&r.BackupID, &r.Service, &r.Host, &r.Engine, &r.Store, &r.Target, &r.SnapshotID,
		&r.SizeBytes, (*string)(&r.Status), (*string)(&r.TriggeredBy), &r.Error,
		&startedMs, &finishedMs,
	); err != nil {
		return BackupRecord{}, fmt.Errorf("scan backup: %w", err)
	}
	r.StartedAt = time.UnixMilli(startedMs)
	if finishedMs.Valid {
		t := time.UnixMilli(finishedMs.Int64)
		r.FinishedAt = &t
	}
	return r, nil
}
