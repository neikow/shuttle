package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/neikow/shuttle/internal/ledger"
	"github.com/neikow/shuttle/internal/webhook"
	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
)

// HTTPServer exposes the orchestrator control plane over HTTP.
type HTTPServer struct {
	token    string
	ledger   *ledger.Store
	registry *Registry
	mux      *http.ServeMux

	webhook *webhook.Handler
	syncer  *GitSyncer
	enroll  *EnrollOptions
}

// EnableWebhook registers POST /webhook, which validates the signed payload and
// triggers a git sync + reconcile. Call before serving.
func (s *HTTPServer) EnableWebhook(h *webhook.Handler, syncer *GitSyncer) {
	s.webhook = h
	s.syncer = syncer
	s.mux.HandleFunc("POST /webhook", s.handleWebhook)
	s.mux.HandleFunc("POST /prune", s.bearerAuth(s.handlePrune))
}

// handlePrune force-deletes the volumes of every removed service that still has
// them (the "manual" delete_volumes policy, and durations not yet elapsed).
func (s *HTTPServer) handlePrune(w http.ResponseWriter, r *http.Request) {
	pruned, err := s.syncer.PruneVolumes(r.Context())
	if err != nil {
		http.Error(w, "prune: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"pruned": pruned})
}

func NewHTTPServer(token string, store *ledger.Store, registry *Registry) *HTTPServer {
	s := &HTTPServer{
		token:    token,
		ledger:   store,
		registry: registry,
		mux:      http.NewServeMux(),
	}
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /deploys", s.bearerAuth(s.handleListDeploys))
	s.mux.HandleFunc("POST /deploy/{service}", s.bearerAuth(s.handleDeploy))
	s.mux.HandleFunc("POST /rollback", s.bearerAuth(s.handleRollback))
	return s
}

func (s *HTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *HTTPServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *HTTPServer) handleListDeploys(w http.ResponseWriter, r *http.Request) {
	service := r.URL.Query().Get("service")
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	deploys, err := s.ledger.ListDeploys(r.Context(), service, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(deploys)
}

func (s *HTTPServer) handleDeploy(w http.ResponseWriter, r *http.Request) {
	service := r.PathValue("service")
	if service == "" {
		http.Error(w, "service required", http.StatusBadRequest)
		return
	}
	sha := r.URL.Query().Get("sha")
	if sha == "" {
		http.Error(w, "sha required", http.StatusBadRequest)
		return
	}

	// Preferred path: render real compose from the repo at the requested SHA.
	if s.syncer != nil {
		deployID, host, err := s.syncer.DeployAtSHA(r.Context(), service, sha, ledger.TriggeredByManual)
		if err != nil {
			writeDeployError(w, err)
			return
		}
		slog.Info("deploy queued", "deploy_id", deployID, "service", service, "host", host, "sha", sha)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"deploy_id": deployID, "host": host})
		return
	}

	// Legacy path (no git sync configured): dispatch without compose. The agent
	// can only act on this if it already has the project on disk.
	host := r.URL.Query().Get("host")
	if host == "" {
		http.Error(w, "host required", http.StatusBadRequest)
		return
	}

	deployID := newID()
	rec := ledger.DeployRecord{
		DeployID:    deployID,
		Service:     service,
		Host:        host,
		SHA:         sha,
		Status:      ledger.StatusPending,
		TriggeredBy: ledger.TriggeredByManual,
		StartedAt:   time.Now(),
	}
	if err := s.ledger.RecordDeploy(r.Context(), rec); err != nil {
		http.Error(w, "record deploy: "+err.Error(), http.StatusInternalServerError)
		return
	}

	cmd := &shuttlev1.OrchestratorCommand{
		Payload: &shuttlev1.OrchestratorCommand_Deploy{
			Deploy: &shuttlev1.DeployRequest{
				DeployId: deployID,
				Service:  service,
				Sha:      sha,
			},
		},
	}
	if err := s.registry.Send(host, cmd); err != nil {
		_ = s.ledger.MarkStatus(r.Context(), deployID, ledger.StatusFailed)
		http.Error(w, "send to agent: "+err.Error(), http.StatusBadGateway)
		return
	}

	slog.Info("deploy queued", "deploy_id", deployID, "service", service, "host", host)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"deploy_id": deployID})
}

func (s *HTTPServer) handleRollback(w http.ResponseWriter, r *http.Request) {
	service := r.URL.Query().Get("service")
	if service == "" {
		http.Error(w, "service required", http.StatusBadRequest)
		return
	}
	stepsStr := r.URL.Query().Get("steps")
	steps := 1
	if stepsStr != "" {
		if n, err := strconv.Atoi(stepsStr); err == nil && n > 0 {
			steps = n
		}
	}
	targetSHA, err := s.ledger.RollbackTarget(r.Context(), service, steps)
	if err != nil {
		http.Error(w, "rollback target: "+err.Error(), http.StatusConflict)
		return
	}

	// Preferred path: render the target SHA's compose from the repo.
	if s.syncer != nil {
		deployID, host, err := s.syncer.DeployAtSHA(r.Context(), service, targetSHA, ledger.TriggeredByRollback)
		if err != nil {
			writeDeployError(w, err)
			return
		}
		slog.Info("rollback queued", "deploy_id", deployID, "service", service, "host", host, "target_sha", targetSHA)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"deploy_id": deployID, "target_sha": targetSHA, "host": host})
		return
	}

	host := r.URL.Query().Get("host")
	if host == "" {
		http.Error(w, "host required", http.StatusBadRequest)
		return
	}

	deployID := newID()
	rec := ledger.DeployRecord{
		DeployID:    deployID,
		Service:     service,
		Host:        host,
		SHA:         targetSHA,
		Status:      ledger.StatusPending,
		TriggeredBy: ledger.TriggeredByRollback,
		StartedAt:   time.Now(),
	}
	if err := s.ledger.RecordDeploy(r.Context(), rec); err != nil {
		http.Error(w, "record rollback: "+err.Error(), http.StatusInternalServerError)
		return
	}

	cmd := &shuttlev1.OrchestratorCommand{
		Payload: &shuttlev1.OrchestratorCommand_Rollback{
			Rollback: &shuttlev1.RollbackRequest{
				DeployId:  deployID,
				Service:   service,
				TargetSha: targetSHA,
			},
		},
	}
	if err := s.registry.Send(host, cmd); err != nil {
		_ = s.ledger.MarkStatus(r.Context(), deployID, ledger.StatusFailed)
		http.Error(w, "send rollback to agent: "+err.Error(), http.StatusBadGateway)
		return
	}

	slog.Info("rollback queued", "deploy_id", deployID, "service", service, "target_sha", targetSHA)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"deploy_id": deployID, "target_sha": targetSHA})
}

// handleWebhook validates the signed payload, then reconciles asynchronously
// (git clone/pull can be slow) and returns 202 immediately.
func (s *HTTPServer) handleWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := s.webhook.Parse(r)
	if err != nil {
		http.Error(w, "webhook: "+err.Error(), http.StatusBadRequest)
		return
	}
	services := payload.Services
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		ids, err := s.syncer.Reconcile(ctx, services)
		if err != nil {
			slog.Error("webhook reconcile failed", "err", err)
			return
		}
		slog.Info("webhook reconcile dispatched", "count", len(ids), "sha", payload.CommitSHA)
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
}

// writeDeployError maps a DeployAtSHA failure to an HTTP status: unknown
// service → 404, anything else (git, agent send) → 502.
func writeDeployError(w http.ResponseWriter, err error) {
	if strings.Contains(err.Error(), "not found") {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusBadGateway)
}

func (s *HTTPServer) bearerAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || auth[7:] != s.token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// newID generates a time-ordered unique ID. Uses context to avoid importing ulid.
func newID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// Ensure HTTPServer satisfies http.Handler.
var _ http.Handler = (*HTTPServer)(nil)

