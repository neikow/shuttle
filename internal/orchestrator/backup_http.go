package orchestrator

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/neikow/shuttle/internal/ledger"
)

// EnableBackups registers the service-backup control-plane endpoints:
//
//	POST   /backup/{service}    (deploy) — trigger a backup now
//	GET    /backups             (read)   — list backup attempts (?service=, ?limit=)
//	GET    /backups/{id}/logs   (read)   — captured output of one backup/restore
//	POST   /restore             (admin)  — restore a service from a backup
//
// Backups are a deploy-tier action (operational, like deploy/rollback); restore
// is admin-tier because it overwrites live data. The dispatch functions are kept
// as fields so tests can substitute stubs without a real GitSyncer.
func (s *HTTPServer) EnableBackups(syncer *GitSyncer) {
	s.syncer = syncer
	s.backupDispatcher = syncer.BackupService
	s.restoreDispatcher = syncer.RestoreService
	s.mux.HandleFunc("POST /backup/{service}", s.requireRole(RoleDeploy, s.handleBackup))
	s.mux.HandleFunc("GET /backups", s.requireRole(RoleRead, s.handleListBackups))
	s.mux.HandleFunc("GET /backups/{id}/logs", s.requireRole(RoleRead, s.handleBackupLogs))
	s.mux.HandleFunc("POST /restore", s.requireRole(RoleAdmin, s.handleRestore))
}

func (s *HTTPServer) handleBackup(w http.ResponseWriter, r *http.Request) {
	service := r.PathValue("service")
	if service == "" {
		http.Error(w, "service required", http.StatusBadRequest)
		return
	}
	if s.backupDispatcher == nil {
		http.Error(w, "backups not configured", http.StatusBadRequest)
		return
	}
	backupID, host, err := s.backupDispatcher(r.Context(), service, ledger.TriggeredByManual)
	if err != nil {
		s.recordAudit(r.Context(), ledger.AuditEntry{
			Actor: auditActor(r), Action: auditBackup, Target: service, SourceIP: clientIP(r),
			Result: auditFailure, Detail: "err=" + err.Error(),
		})
		writeDeployError(w, err)
		return
	}
	s.recordAudit(r.Context(), ledger.AuditEntry{
		Actor: auditActor(r), Action: auditBackup, Target: service, SourceIP: clientIP(r),
		Result: auditSuccess, Detail: "backup_id=" + backupID + " host=" + host,
	})
	slog.Info("backup queued", "backup_id", backupID, "service", service, "host", host)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"backup_id": backupID, "host": host})
}

func (s *HTTPServer) handleListBackups(w http.ResponseWriter, r *http.Request) {
	service := r.URL.Query().Get("service")
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	backups, err := s.ledger.ListBackups(r.Context(), service, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if backups == nil {
		backups = []ledger.BackupRecord{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(backups)
}

func (s *HTTPServer) handleBackupLogs(w http.ResponseWriter, r *http.Request) {
	logs, err := s.ledger.DeployLogs(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(logs)
}

func (s *HTTPServer) handleRestore(w http.ResponseWriter, r *http.Request) {
	service := r.URL.Query().Get("service")
	if service == "" {
		http.Error(w, "service required", http.StatusBadRequest)
		return
	}
	if s.restoreDispatcher == nil {
		http.Error(w, "backups not configured", http.StatusBadRequest)
		return
	}
	backupID := r.URL.Query().Get("backup_id") // empty => latest successful
	opID, host, snapshot, err := s.restoreDispatcher(r.Context(), service, backupID)
	if err != nil {
		s.recordAudit(r.Context(), ledger.AuditEntry{
			Actor: auditActor(r), Action: auditRestore, Target: service, SourceIP: clientIP(r),
			Result: auditFailure, Detail: "backup_id=" + backupID + " err=" + err.Error(),
		})
		writeDeployError(w, err)
		return
	}
	s.recordAudit(r.Context(), ledger.AuditEntry{
		Actor: auditActor(r), Action: auditRestore, Target: service, SourceIP: clientIP(r),
		Result: auditSuccess, Detail: "operation_id=" + opID + " snapshot=" + snapshot + " host=" + host,
	})
	slog.Info("restore queued", "operation_id", opID, "service", service, "host", host, "snapshot", snapshot)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"operation_id": opID, "host": host, "snapshot_id": snapshot})
}

// Compile-time guard that the dispatcher fields match the GitSyncer methods.
var (
	_ func(context.Context, string, ledger.TriggeredBy) (string, string, error) = (*GitSyncer)(nil).BackupService
	_ func(context.Context, string, string) (string, string, string, error)     = (*GitSyncer)(nil).RestoreService
)
