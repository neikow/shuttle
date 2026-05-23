package ledger

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed *.sql
var embedMigrations embed.FS

type Status string

const (
	StatusPending    Status = "pending"
	StatusRunning    Status = "running"
	StatusSuccess    Status = "success"
	StatusFailed     Status = "failed"
	StatusRolledBack Status = "rolled_back"
)

type TriggeredBy string

const (
	TriggeredByWebhook  TriggeredBy = "webhook"
	TriggeredByManual   TriggeredBy = "manual"
	TriggeredByRollback TriggeredBy = "rollback"
)

type DeployRecord struct {
	DeployID     string
	Service      string
	Host         string
	SHA          string
	Status       Status
	TriggeredBy  TriggeredBy
	WebhookNonce string
	StartedAt    time.Time
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	dsn := path + "?_journal_mode=WAL&_foreign_keys=on"
	if path == ":memory:" {
		// In-memory SQLite creates a new DB per connection; pin to one.
		dsn = "file::memory:?cache=shared&_journal_mode=WAL&_foreign_keys=on"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer; cap open conns to avoid lock contention.
	db.SetMaxOpenConns(1)
	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrations: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func runMigrations(db *sql.DB) error {
	goose.SetBaseFS(embedMigrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		return err
	}
	return goose.Up(db, ".", goose.WithNoVersioning())
}

func (s *Store) RecordDeploy(ctx context.Context, r DeployRecord) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO deploys (deploy_id, service, host, sha, status, triggered_by, webhook_nonce, started_at)
		 VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?)`,
		r.DeployID, r.Service, r.Host, r.SHA, string(r.Status), string(r.TriggeredBy),
		r.WebhookNonce, r.StartedAt.UnixMilli(),
	)
	return err
}

func (s *Store) MarkStatus(ctx context.Context, deployID string, status Status) error {
	var finishedAt *int64
	if status == StatusSuccess || status == StatusFailed || status == StatusRolledBack {
		t := time.Now().UnixMilli()
		finishedAt = &t
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE deploys SET status = ?, finished_at = ? WHERE deploy_id = ?`,
		string(status), finishedAt, deployID,
	)
	return err
}

// RollbackTarget returns the SHA that is `steps` successful deploys before the latest.
func (s *Store) RollbackTarget(ctx context.Context, service string, steps int) (string, error) {
	if steps < 1 {
		return "", fmt.Errorf("steps must be >= 1")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT sha FROM deploys WHERE service = ? AND status = 'success'
		 ORDER BY started_at DESC LIMIT ?`,
		service, steps+1,
	)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()

	var shas []string
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return "", err
		}
		shas = append(shas, sha)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(shas) <= steps {
		return "", fmt.Errorf("not enough history: have %d successful deploys, need %d", len(shas), steps+1)
	}
	return shas[steps], nil
}

// SeenNonce returns true if the nonce was already seen within ttl, otherwise records it.
func (s *Store) SeenNonce(ctx context.Context, nonce string, ttl time.Duration) (bool, error) {
	cutoff := time.Now().Add(-ttl).UnixMilli()
	// Purge expired nonces.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM webhook_nonces WHERE seen_at < ?`, cutoff); err != nil {
		return false, err
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO webhook_nonces (nonce, seen_at) VALUES (?, ?)`,
		nonce, time.Now().UnixMilli(),
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 0, nil // 0 rows inserted = already seen
}

// ListDeploys returns the last `limit` deploys for a service (all services if service is "").
// CurrentSHAs returns the latest successfully-deployed SHA per service.
// SQLite returns the bare columns from the row holding MAX(started_at).
func (s *Store) CurrentSHAs(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT service, sha, MAX(started_at) FROM deploys
		 WHERE status = ? GROUP BY service`, StatusSuccess)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]string)
	for rows.Next() {
		var service, sha string
		var maxStarted int64
		if err := rows.Scan(&service, &sha, &maxStarted); err != nil {
			return nil, err
		}
		out[service] = sha
	}
	return out, rows.Err()
}

func (s *Store) ListDeploys(ctx context.Context, service string, limit int) ([]DeployRecord, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if service == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT deploy_id, service, host, sha, status, triggered_by, COALESCE(webhook_nonce,''), started_at
			 FROM deploys ORDER BY started_at DESC LIMIT ?`, limit)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT deploy_id, service, host, sha, status, triggered_by, COALESCE(webhook_nonce,''), started_at
			 FROM deploys WHERE service = ? ORDER BY started_at DESC LIMIT ?`, service, limit)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []DeployRecord
	for rows.Next() {
		var r DeployRecord
		var startedAtMs int64
		if err := rows.Scan(&r.DeployID, &r.Service, &r.Host, &r.SHA,
			(*string)(&r.Status), (*string)(&r.TriggeredBy), &r.WebhookNonce, &startedAtMs); err != nil {
			return nil, err
		}
		r.StartedAt = time.UnixMilli(startedAtMs)
		out = append(out, r)
	}
	return out, rows.Err()
}
