package config

import (
	"fmt"
	"strings"
	"time"
)

// Backup engines (how a service's data is captured).
const (
	BackupEngineVolume   = "volume"   // tar/snapshot the project's named volumes
	BackupEnginePostgres = "postgres" // pg_dump from the service's database container
)

// Backup stores (where captured data is written).
const (
	BackupStoreLocal  = "local"  // a plain file under a directory on the agent host
	BackupStoreRestic = "restic" // a restic repository (local path, S3, B2, rclone, ...)
)

// ServiceBackup is a service's repo-managed backup policy. It is desired state
// (lives in the IaC repo beside the service) — the backend *credentials* are
// not, and are resolved from the secrets provider at dispatch time (see the
// orchestrator's BackupConfig). Store/Target may be left empty to inherit the
// orchestrator's bootstrap defaults.
type ServiceBackup struct {
	Engine string // "volume" | "postgres"
	Store  string // "local" | "restic"; empty inherits the bootstrap default
	Target string // local dir or restic repo; empty inherits the bootstrap default
	// Schedule is how often a scheduled backup runs: a duration/keyword
	// ("24h", "daily", "weekly") interpreted by ScheduleInterval. Empty disables
	// scheduled backups (manual/pre-deploy backups still work).
	Schedule string
	// BeforeDeploy, when true, takes a best-effort snapshot immediately before
	// each deploy/rollback of the service, so a bad release has a fresh restore
	// point. Best-effort: a failed pre-deploy backup is logged, never blocks.
	BeforeDeploy bool
	// Volumes restricts a "volume" engine backup to these compose volume keys;
	// empty backs up every named volume in the project.
	Volumes []string
	// Postgres engine parameters.
	DBService string // compose service name of the database container
	DBUser    string
	DBName    string // empty => pg_dumpall
	Retention BackupRetention
}

// BackupRetention bounds how many restic snapshots are kept after each backup
// (passed to `restic forget`). Zero fields mean keep everything. The local store
// ignores retention.
type BackupRetention struct {
	KeepLast    int `yaml:"keep_last"`
	KeepDaily   int `yaml:"keep_daily"`
	KeepWeekly  int `yaml:"keep_weekly"`
	KeepMonthly int `yaml:"keep_monthly"`
}

// serviceBackup is the YAML form of a service's backup block.
type serviceBackup struct {
	Engine       string          `yaml:"engine"`
	Store        string          `yaml:"store"`
	Target       string          `yaml:"target"`
	Schedule     string          `yaml:"schedule"`
	BeforeDeploy bool            `yaml:"before_deploy"`
	Volumes      []string        `yaml:"volumes"`
	DBService    string          `yaml:"db_service"`
	DBUser       string          `yaml:"db_user"`
	DBName       string          `yaml:"db_name"`
	Retention    BackupRetention `yaml:"retention"`
}

// normalizeBackup validates a service's backup block and returns its canonical
// form, or nil when no block is present.
func normalizeBackup(raw *serviceBackup) (*ServiceBackup, error) {
	if raw == nil {
		return nil, nil
	}
	b := &ServiceBackup{
		Engine:       strings.ToLower(strings.TrimSpace(raw.Engine)),
		Store:        strings.ToLower(strings.TrimSpace(raw.Store)),
		Target:       strings.TrimSpace(raw.Target),
		Schedule:     strings.TrimSpace(raw.Schedule),
		BeforeDeploy: raw.BeforeDeploy,
		Volumes:      raw.Volumes,
		DBService:    strings.TrimSpace(raw.DBService),
		DBUser:       strings.TrimSpace(raw.DBUser),
		DBName:       strings.TrimSpace(raw.DBName),
		Retention:    raw.Retention,
	}
	switch b.Engine {
	case BackupEngineVolume, BackupEnginePostgres:
	case "":
		return nil, fmt.Errorf("backup.engine is required (want %q or %q)", BackupEngineVolume, BackupEnginePostgres)
	default:
		return nil, fmt.Errorf("backup.engine %q invalid (want %q or %q)", b.Engine, BackupEngineVolume, BackupEnginePostgres)
	}
	switch b.Store {
	case BackupStoreLocal, BackupStoreRestic, "":
	default:
		return nil, fmt.Errorf("backup.store %q invalid (want %q or %q)", b.Store, BackupStoreLocal, BackupStoreRestic)
	}
	if b.Engine == BackupEnginePostgres && b.DBService == "" {
		return nil, fmt.Errorf("backup.db_service is required for the %q engine", BackupEnginePostgres)
	}
	if b.Schedule != "" {
		if _, err := b.ScheduleInterval(); err != nil {
			return nil, fmt.Errorf("backup.schedule: %w", err)
		}
	}
	return b, nil
}

// ScheduleInterval parses Schedule into a polling interval. Besides the human
// durations ParseHumanDuration accepts ("24h", "7 days"), the keywords
// "hourly", "daily", and "weekly" are recognized. An empty schedule yields 0
// (scheduled backups disabled).
func (b *ServiceBackup) ScheduleInterval() (time.Duration, error) {
	s := strings.ToLower(strings.TrimSpace(b.Schedule))
	switch s {
	case "":
		return 0, nil
	case "hourly":
		return time.Hour, nil
	case "daily":
		return 24 * time.Hour, nil
	case "weekly":
		return 7 * 24 * time.Hour, nil
	}
	return ParseHumanDuration(s)
}
