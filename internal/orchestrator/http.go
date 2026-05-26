package orchestrator

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
	"github.com/neikow/shuttle/internal/infisical"
	"github.com/neikow/shuttle/internal/ledger"
	"github.com/neikow/shuttle/internal/webhook"
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

	infisical         *infisical.Handler
	infisicalDebounce *changeDebouncer
	infisicalDefEnv   string // default env for services with no env_from

	bus          *EventBus     // optional; nil-safe
	stateTracker *StateTracker // optional; nil-safe (powers GET /overview)

	// repoWebhookDeployer is the ForceDeploy implementation used by the
	// repo-webhook trigger handler. Kept separate so tests can substitute a
	// stub without needing a real GitSyncer.
	repoWebhookDeployer func(ctx context.Context, services []string) ([]string, error)
}

// SetEventBus attaches the event bus the control plane publishes to and, when
// non-nil, registers the SSE event stream at GET /events. Call before serving.
func (s *HTTPServer) SetEventBus(b *EventBus) {
	s.bus = b
	if b != nil {
		s.mux.HandleFunc("GET /events", s.bearerAuth(s.handleEvents))
	}
}

// sseHeartbeat is how often an idle stream emits a comment line, keeping proxies
// and load balancers from closing the connection.
const sseHeartbeat = 25 * time.Second

// handleEvents streams orchestrator events to the client as Server-Sent Events.
// On connect it replays the bus backlog, then forwards live events until the
// client disconnects. Each event is one `data: <json>` frame; the JSON carries
// the type, so a client filters on `.type` from a single EventSource.
func (s *HTTPServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	sub, backlog := s.bus.Subscribe()
	defer sub.Close()

	for _, ev := range backlog {
		if err := writeSSE(w, ev); err != nil {
			return
		}
	}
	flusher.Flush()

	ctx := r.Context()
	heartbeat := time.NewTicker(sseHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-sub.C:
			if !open {
				return
			}
			if err := writeSSE(w, ev); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// writeSSE encodes one event as an SSE data frame.
func writeSSE(w io.Writer, ev Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}

// EnableMetrics registers GET /metrics, serving Prometheus metrics. Unauthed by
// design: the exposed metrics are low-cardinality aggregates (no service/host
// labels), matching the standard scrape model. Call before serving.
func (s *HTTPServer) EnableMetrics(h http.Handler) {
	s.mux.Handle("GET /metrics", h)
}

// EnableWebhook registers POST /webhook, which validates the signed payload and
// triggers a git sync + reconcile. Call before serving.
func (s *HTTPServer) EnableWebhook(h *webhook.Handler, syncer *GitSyncer) {
	s.webhook = h
	s.syncer = syncer
	s.mux.HandleFunc("POST /webhook", s.handleWebhook)
	s.mux.HandleFunc("POST /prune", s.bearerAuth(s.handlePrune))
	s.mux.HandleFunc("GET /plan", s.bearerAuth(s.handlePlan))
	s.mux.HandleFunc("GET /check", s.bearerAuth(s.handleCheck))
}

// handleCheck runs the read-only config + secret-availability validation pass
// against the orchestrator's own repo + secrets provider, returning the report
// as JSON. Dispatches nothing.
func (s *HTTPServer) handleCheck(w http.ResponseWriter, r *http.Request) {
	if s.syncer == nil {
		http.Error(w, "git sync not configured", http.StatusBadRequest)
		return
	}
	report, err := s.syncer.CheckRef(r.Context(), r.URL.Query().Get("ref"))
	if err != nil {
		http.Error(w, "check: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}

// handlePlan returns the read-only desired-vs-actual diff (what a reconcile
// would do) as JSON. Dispatches nothing.
func (s *HTTPServer) handlePlan(w http.ResponseWriter, r *http.Request) {
	if s.syncer == nil {
		http.Error(w, "git sync not configured", http.StatusBadRequest)
		return
	}
	report, err := s.syncer.PlanRef(r.Context(), r.URL.Query().Get("ref"))
	if err != nil {
		http.Error(w, "plan: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}

// EnableInfisicalWebhook registers POST /webhook/infisical, which authenticates
// an Infisical secret-change webhook, maps the changed secret to the services
// that read it, and redeploys only those — debounced so a burst of edits
// triggers one reconcile pass. defaultEnv resolves services with no env_from.
// Call before serving.
func (s *HTTPServer) EnableInfisicalWebhook(h *infisical.Handler, syncer *GitSyncer, debounce time.Duration, defaultEnv string) {
	s.infisical = h
	s.syncer = syncer
	s.infisicalDefEnv = defaultEnv
	s.infisicalDebounce = newChangeDebouncer(debounce, s.reconcileSecretChanges)
	s.mux.HandleFunc("POST /webhook/infisical", s.handleInfisicalWebhook)
}

// handleInfisicalWebhook validates the signed payload and queues a debounced
// redeploy of the affected services, returning 202 immediately.
func (s *HTTPServer) handleInfisicalWebhook(w http.ResponseWriter, r *http.Request) {
	slog.Debug("infisical webhook received",
		"method", r.Method,
		"content_type", r.Header.Get("Content-Type"),
		"signature_header", r.Header.Get("x-infisical-signature"),
		"user_agent", r.Header.Get("User-Agent"),
	)
	payload, err := s.infisical.Parse(r)
	if err != nil {
		slog.Debug("infisical webhook parse failed", "err", err)
		http.Error(w, "infisical webhook: "+err.Error(), http.StatusBadRequest)
		return
	}
	slog.Info("infisical webhook event",
		"event", payload.Event,
		"env", payload.Env(),
		"path", payload.Path(),
		"changed_by", payload.Project.ChangedBy,
		"actor_type", payload.Project.ChangedByActorType,
	)
	slog.Debug("infisical webhook parsed",
		"event", payload.Event,
		"env", payload.Env(),
		"path", payload.Path(),
		"project_env", payload.Project.Environment,
		"project_path", payload.Project.SecretPath,
		"top_level_env", payload.Environment,
		"top_level_path", payload.SecretPath,
	)
	switch payload.Event {
	case infisical.EventTest:
		slog.Info("infisical test ping received")
		w.WriteHeader(http.StatusOK)
		return
	case infisical.EventSecretsRotationFailed:
		slog.Warn("infisical secret rotation failed",
			"env", payload.Env(),
			"path", payload.Path(),
			"rotation", payload.Project.RotationName,
			"error", payload.Project.ErrorMessage,
		)
		w.WriteHeader(http.StatusOK)
		return
	case infisical.EventSecretsModified:
		// fall through to reconcile
	default:
		slog.Warn("infisical webhook: unrecognised event type; ignoring", "event", payload.Event)
		w.WriteHeader(http.StatusOK)
		return
	}
	s.infisicalDebounce.Trigger(SecretChange{Env: payload.Env(), Path: payload.Path()})
	slog.Info("infisical change queued", "event", payload.Event, "env", payload.Env(), "path", payload.Path())

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
}

// reconcileSecretChanges resolves the union of services affected by the given
// secret changes and reconciles them. It runs off the request path (debounced),
// so it manages its own context and only logs failures.
func (s *HTTPServer) reconcileSecretChanges(changes []SecretChange) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	affected := make(map[string]struct{})
	for _, c := range changes {
		svcs, err := s.syncer.ServicesUsingSecret(ctx, c.Env, c.Path, s.infisicalDefEnv)
		if err != nil {
			slog.Error("infisical resolve affected services failed", "env", c.Env, "path", c.Path, "err", err)
			return
		}
		for _, svc := range svcs {
			affected[svc] = struct{}{}
		}
	}
	if len(affected) == 0 {
		slog.Info("infisical change affected no services", "changes", len(changes))
		return
	}
	services := make([]string, 0, len(affected))
	for svc := range affected {
		services = append(services, svc)
	}
	// A secret change does not move the repo SHA, so the SHA-gated Reconcile
	// would find no work and re-render nothing. ForceDeploy redeploys the
	// affected services at HEAD regardless of ledger SHA, re-rendering their
	// .env with the new secret values.
	ids, err := s.syncer.ForceDeploy(ctx, services)
	if err != nil {
		slog.Error("infisical reconcile failed", "err", err)
		return
	}
	slog.Info("infisical reconcile dispatched", "services", services, "count", len(ids))
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
	s.mux.HandleFunc("GET /overview", s.bearerAuth(s.handleOverview))
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
		s.bus.Publish(Event{
			Type: EventDeployFailed, Service: service, Host: host, DeployID: deployID,
			SHA: sha, Status: string(ledger.StatusFailed), Message: "send to agent failed",
		})
		http.Error(w, "send to agent: "+err.Error(), http.StatusBadGateway)
		return
	}

	slog.Info("deploy queued", "deploy_id", deployID, "service", service, "host", host)
	s.bus.Publish(Event{
		Type: EventDeployQueued, Service: service, Host: host, DeployID: deployID,
		SHA: sha, Status: string(ledger.StatusPending),
		Detail: map[string]string{"triggered_by": string(ledger.TriggeredByManual)},
	})
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
		s.bus.Publish(Event{
			Type: EventDeployFailed, Service: service, Host: host, DeployID: deployID,
			SHA: targetSHA, Status: string(ledger.StatusFailed), Message: "send rollback to agent failed",
		})
		http.Error(w, "send rollback to agent: "+err.Error(), http.StatusBadGateway)
		return
	}

	s.bus.Publish(Event{
		Type: EventRollbackQueued, Service: service, Host: host, DeployID: deployID,
		SHA: targetSHA, Status: string(ledger.StatusPending),
		Detail: map[string]string{"triggered_by": string(ledger.TriggeredByRollback)},
	})
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
		if !strings.HasPrefix(auth, "Bearer ") || !constantTimeEqual(auth[len("Bearer "):], s.token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// constantTimeEqual compares two secrets without leaking their length or content
// through timing. Both sides are hashed to a fixed-size digest first, so the
// comparison is constant-time regardless of input length.
func constantTimeEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

// newID generates a time-ordered unique ID. Uses context to avoid importing ulid.
func newID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// EnableRepoWebhooks registers the repo-webhook management and trigger endpoints.
func (s *HTTPServer) EnableRepoWebhooks(syncer *GitSyncer) {
	s.syncer = syncer
	s.repoWebhookDeployer = syncer.ForceDeploy
	s.mux.HandleFunc("POST /webhook/repo/{id}", s.handleRepoWebhookTrigger)
	s.mux.HandleFunc("POST /webhooks/repo", s.bearerAuth(s.handleCreateRepoWebhook))
	s.mux.HandleFunc("GET /webhooks/repo", s.bearerAuth(s.handleListRepoWebhooks))
	s.mux.HandleFunc("DELETE /webhooks/repo/{id}", s.bearerAuth(s.handleDeleteRepoWebhook))
}

// handleRepoWebhookTrigger fires a ForceDeploy for the service bound to the
// webhook ID. No bearer auth — the 256-bit random ID is the secret.
func (s *HTTPServer) handleRepoWebhookTrigger(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	service, err := s.ledger.LookupRepoWebhook(r.Context(), id)
	if err != nil {
		http.Error(w, "webhook not found", http.StatusNotFound)
		return
	}
	// Drain body (ignore payload; ID entropy is sufficient for auth).
	_, _ = io.Copy(io.Discard, r.Body)

	deployIDs, err := s.repoWebhookDeployer(r.Context(), []string{service})
	if err != nil {
		slog.Error("repo webhook deploy failed", "webhook_id", id, "service", service, "err", err)
		http.Error(w, "deploy failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	slog.Info("repo webhook triggered", "webhook_id", id, "service", service, "deploy_ids", deployIDs)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"deploy_ids": deployIDs})
}

func (s *HTTPServer) handleCreateRepoWebhook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Service string `json:"service"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Service == "" {
		http.Error(w, "service required", http.StatusBadRequest)
		return
	}
	id, err := s.ledger.CreateRepoWebhook(r.Context(), req.Service)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
}

func (s *HTTPServer) handleListRepoWebhooks(w http.ResponseWriter, r *http.Request) {
	webhooks, err := s.ledger.ListRepoWebhooks(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if webhooks == nil {
		webhooks = []ledger.RepoWebhook{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(webhooks)
}

func (s *HTTPServer) handleDeleteRepoWebhook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.ledger.DeleteRepoWebhook(r.Context(), id); err != nil {
		if errors.As(err, new(ledger.ErrWebhookNotFound)) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Ensure HTTPServer satisfies http.Handler.
var _ http.Handler = (*HTTPServer)(nil)
