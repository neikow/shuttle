package ledger

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

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

// runMigrations applies every embedded *.sql migration in filename order inside
// a single transaction. The migrations are written idempotently
// (CREATE ... IF NOT EXISTS), so replaying the full set on each open is safe and
// no version table is needed — matching the prior goose WithNoVersioning() mode
// without the dependency. Only the "+goose Up" section of each file is executed.
func runMigrations(db *sql.DB) error {
	ctx := context.Background()
	names, err := fs.Glob(embedMigrations, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, name := range names {
		raw, err := embedMigrations.ReadFile(name)
		if err != nil {
			return err
		}
		stmt := strings.TrimSpace(upSection(string(raw)))
		if stmt == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return tx.Commit()
}

// upSection extracts the statements between "-- +goose Up" and "-- +goose Down"
// (or end of file), dropping any "-- +goose" annotation lines. Files have no
// Down section that runs here; the markers are kept only so the .sql files stay
// readable as standalone migrations.
func upSection(content string) string {
	var out []string
	inUp := false
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "-- +goose Up"):
			inUp = true
			continue
		case strings.HasPrefix(trimmed, "-- +goose Down"):
			inUp = false
			continue
		case strings.HasPrefix(trimmed, "-- +goose"):
			continue
		}
		if inUp {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
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

// RepoWebhook is a record in the repo_webhooks table.
type RepoWebhook struct {
	ID        string
	Service   string
	CreatedAt time.Time
}

// ErrWebhookNotFound is returned when a webhook ID does not exist.
type ErrWebhookNotFound struct{ ID string }

func (e ErrWebhookNotFound) Error() string { return "repo webhook not found: " + e.ID }

// CreateRepoWebhook generates a cryptographically random 32-byte hex ID,
// inserts a webhook record for the given service, and returns the ID.
func (s *Store) CreateRepoWebhook(ctx context.Context, service string) (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate webhook id: %w", err)
	}
	id := hex.EncodeToString(buf[:])
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO repo_webhooks (id, service, created_at) VALUES (?, ?, ?)`,
		id, service, time.Now().UnixMilli(),
	)
	if err != nil {
		return "", fmt.Errorf("insert repo webhook: %w", err)
	}
	return id, nil
}

// LookupRepoWebhook returns the service name for the given webhook ID.
// Returns ErrWebhookNotFound if not found.
func (s *Store) LookupRepoWebhook(ctx context.Context, id string) (string, error) {
	var service string
	err := s.db.QueryRowContext(ctx, `SELECT service FROM repo_webhooks WHERE id = ?`, id).Scan(&service)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrWebhookNotFound{ID: id}
	}
	if err != nil {
		return "", fmt.Errorf("lookup repo webhook: %w", err)
	}
	return service, nil
}

// ListRepoWebhooks returns all registered repo webhooks ordered by creation time.
func (s *Store) ListRepoWebhooks(ctx context.Context) ([]RepoWebhook, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, service, created_at FROM repo_webhooks ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list repo webhooks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []RepoWebhook
	for rows.Next() {
		var w RepoWebhook
		var ms int64
		if err := rows.Scan(&w.ID, &w.Service, &ms); err != nil {
			return nil, fmt.Errorf("scan repo webhook: %w", err)
		}
		w.CreatedAt = time.UnixMilli(ms)
		out = append(out, w)
	}
	return out, rows.Err()
}

// DeleteRepoWebhook removes the webhook with the given ID.
// Returns ErrWebhookNotFound if it doesn't exist.
func (s *Store) DeleteRepoWebhook(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM repo_webhooks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete repo webhook: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrWebhookNotFound{ID: id}
	}
	return nil
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
