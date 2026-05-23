package ledger

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ServiceLifecycle is a row of the service_lifecycle table.
type ServiceLifecycle struct {
	Service             string
	Host                string
	DeleteVolumes       string
	Present             bool
	RemovedAt           *int64 // epoch ms
	ContainersRemovedAt *int64 // epoch ms
	VolumesPurgeAfter   *int64 // epoch ms; nil = no scheduled purge (manual)
	VolumesPurgedAt     *int64 // epoch ms
}

// MarkServicePresent upserts a service as present (in the repo). On a service
// that was previously removed, this resets the removal lifecycle so a re-added
// service starts clean. deleteVolumes is the service's last-known volume policy,
// captured here so it survives the service's later removal from the repo.
func (s *Store) MarkServicePresent(ctx context.Context, service, host, deleteVolumes string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO service_lifecycle
		   (service, host, delete_volumes, present, removed_at, containers_removed_at,
		    volumes_purge_after, volumes_purged_at, updated_at)
		 VALUES (?, ?, ?, 1, NULL, NULL, NULL, NULL, ?)
		 ON CONFLICT(service) DO UPDATE SET
		   host = excluded.host,
		   delete_volumes = excluded.delete_volumes,
		   present = 1,
		   removed_at = NULL,
		   containers_removed_at = NULL,
		   volumes_purge_after = NULL,
		   volumes_purged_at = NULL,
		   updated_at = excluded.updated_at`,
		service, host, deleteVolumes, now,
	)
	return err
}

// PresentServices returns the names of services currently marked present.
func (s *Store) PresentServices(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT service FROM service_lifecycle WHERE present = 1`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var svc string
		if err := rows.Scan(&svc); err != nil {
			return nil, err
		}
		out = append(out, svc)
	}
	return out, rows.Err()
}

// ServiceDeleteVolumes returns a service's last-known delete_volumes policy,
// captured while it was present. Defaults to "manual" if the service is unknown.
func (s *Store) ServiceDeleteVolumes(ctx context.Context, service string) (string, error) {
	var policy string
	err := s.db.QueryRowContext(ctx,
		`SELECT delete_volumes FROM service_lifecycle WHERE service = ?`, service).Scan(&policy)
	if errors.Is(err, sql.ErrNoRows) {
		return "manual", nil
	}
	if err != nil {
		return "", err
	}
	return policy, nil
}

// MarkServiceRemoved flips a service to absent and stamps removed_at (only if not
// already set, so repeated reconciles keep the original removal time). purgeAfter,
// when non-nil, schedules volume deletion for that epoch-ms instant; nil leaves
// volumes pending an explicit prune.
func (s *Store) MarkServiceRemoved(ctx context.Context, service string, purgeAfter *int64) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx,
		`UPDATE service_lifecycle
		   SET present = 0,
		       removed_at = COALESCE(removed_at, ?),
		       volumes_purge_after = ?,
		       updated_at = ?
		 WHERE service = ?`,
		now, purgeAfter, now, service,
	)
	return err
}

// ServicesAwaitingTeardown returns services that are removed but whose containers
// have not yet been torn down (containers_removed_at is unset). The reconciler
// dispatches a teardown for each; the operation is idempotent, so an agent that
// was offline at removal time is retried on the next tick.
func (s *Store) ServicesAwaitingTeardown(ctx context.Context) ([]ServiceLifecycle, error) {
	return s.queryLifecycle(ctx,
		`SELECT service, host, delete_volumes FROM service_lifecycle
		 WHERE present = 0 AND containers_removed_at IS NULL`)
}

// MarkContainersRemoved stamps containers_removed_at, recording that a teardown
// was dispatched for the service.
func (s *Store) MarkContainersRemoved(ctx context.Context, service string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE service_lifecycle SET containers_removed_at = ?, updated_at = ? WHERE service = ?`,
		time.Now().UnixMilli(), time.Now().UnixMilli(), service,
	)
	return err
}

// ServicesAwaitingPurge returns removed services whose scheduled volume-deletion
// deadline (volumes_purge_after) has passed and whose volumes are not yet purged.
// Services with no deadline (manual policy) are excluded — those wait for prune.
func (s *Store) ServicesAwaitingPurge(ctx context.Context, now int64) ([]ServiceLifecycle, error) {
	return s.queryLifecycle(ctx,
		`SELECT service, host, delete_volumes FROM service_lifecycle
		 WHERE present = 0 AND containers_removed_at IS NOT NULL
		   AND volumes_purged_at IS NULL
		   AND volumes_purge_after IS NOT NULL AND volumes_purge_after <= ?`, now)
}

// ServicesPendingVolumes returns every removed service whose volumes have not yet
// been purged, regardless of policy or deadline. This is the prune set: an
// explicit prune force-deletes all kept volumes now.
func (s *Store) ServicesPendingVolumes(ctx context.Context) ([]ServiceLifecycle, error) {
	return s.queryLifecycle(ctx,
		`SELECT service, host, delete_volumes FROM service_lifecycle
		 WHERE present = 0 AND volumes_purged_at IS NULL`)
}

func (s *Store) queryLifecycle(ctx context.Context, query string, args ...any) ([]ServiceLifecycle, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ServiceLifecycle
	for rows.Next() {
		var sl ServiceLifecycle
		if err := rows.Scan(&sl.Service, &sl.Host, &sl.DeleteVolumes); err != nil {
			return nil, err
		}
		out = append(out, sl)
	}
	return out, rows.Err()
}

// MarkVolumesPurged stamps volumes_purged_at, recording that the service's
// volumes were deleted.
func (s *Store) MarkVolumesPurged(ctx context.Context, service string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx,
		`UPDATE service_lifecycle SET volumes_purged_at = ?, updated_at = ? WHERE service = ?`,
		now, now, service,
	)
	return err
}
