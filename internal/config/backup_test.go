package config

import (
	"testing"
	"time"
)

func TestNormalizeBackup(t *testing.T) {
	tests := []struct {
		name    string
		in      *serviceBackup
		wantErr bool
		check   func(t *testing.T, b *ServiceBackup)
	}{
		{name: "nil is nil", in: nil, check: func(t *testing.T, b *ServiceBackup) {
			if b != nil {
				t.Fatalf("want nil, got %+v", b)
			}
		}},
		{
			name: "volume engine ok",
			in:   &serviceBackup{Engine: "Volume", Store: "Restic", Target: "s3:bucket", Schedule: "daily"},
			check: func(t *testing.T, b *ServiceBackup) {
				if b.Engine != BackupEngineVolume || b.Store != BackupStoreRestic {
					t.Fatalf("not canonicalized: %+v", b)
				}
			},
		},
		{
			name:    "postgres requires db_service",
			in:      &serviceBackup{Engine: "postgres"},
			wantErr: true,
		},
		{
			name: "postgres ok with db_service",
			in:   &serviceBackup{Engine: "postgres", DBService: "db", DBUser: "postgres"},
			check: func(t *testing.T, b *ServiceBackup) {
				if b.DBService != "db" {
					t.Fatalf("db_service lost: %+v", b)
				}
			},
		},
		{name: "missing engine", in: &serviceBackup{Store: "local"}, wantErr: true},
		{name: "bad engine", in: &serviceBackup{Engine: "btrfs"}, wantErr: true},
		{name: "bad store", in: &serviceBackup{Engine: "volume", Store: "rsync"}, wantErr: true},
		{name: "bad schedule", in: &serviceBackup{Engine: "volume", Schedule: "fortnightly"}, wantErr: true},
		{name: "empty store inherits later", in: &serviceBackup{Engine: "volume"}, check: func(t *testing.T, b *ServiceBackup) {
			if b.Store != "" {
				t.Fatalf("store should stay empty for inheritance, got %q", b.Store)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := normalizeBackup(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil (%+v)", b)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, b)
			}
		})
	}
}

func TestScheduleInterval(t *testing.T) {
	tests := []struct {
		schedule string
		want     time.Duration
		wantErr  bool
	}{
		{"", 0, false},
		{"hourly", time.Hour, false},
		{"daily", 24 * time.Hour, false},
		{"weekly", 7 * 24 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"7 days", 7 * 24 * time.Hour, false},
		{"never", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.schedule, func(t *testing.T) {
			b := &ServiceBackup{Schedule: tt.schedule}
			got, err := b.ScheduleInterval()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error for %q", tt.schedule)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("interval(%q) = %v, want %v", tt.schedule, got, tt.want)
			}
		})
	}
}

func TestBackupConfigEnabled(t *testing.T) {
	if (BackupConfig{}).Enabled() {
		t.Fatal("empty BackupConfig should be disabled")
	}
	if !(BackupConfig{DefaultTarget: "/backups"}).Enabled() {
		t.Fatal("default_target should enable backups")
	}
	if !(BackupConfig{Env: []BackupCredential{{Key: "RESTIC_PASSWORD", InfisicalKey: "RP"}}}).Enabled() {
		t.Fatal("env credential should enable backups")
	}
}
