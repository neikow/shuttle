package orchestrator

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/neikow/shuttle/internal/ledger"
)

// Audit action names. These are the stable identifiers written to audit_log and
// usable as the `?action=` filter on GET /audit.
const (
	auditDeploy        = "deploy"
	auditRollback      = "rollback"
	auditPrune         = "prune"
	auditEnroll        = "enroll"
	auditRedeem        = "enroll.redeem"
	auditWebhookCreate = "webhook.create"
	auditWebhookDelete = "webhook.delete"
)

// Audit result values.
const (
	auditSuccess = "success"
	auditFailure = "failure"
)

// auditActor derives the actor for an audited request. With a single static
// bearer token the orchestrator cannot tell individual operators apart, so a
// caller may self-identify via the X-Actor header (e.g. CI sets it to the
// triggering user/workflow); absent that, the actor is the generic "operator".
//
// Source IP for audit entries comes from clientIP (ratelimit.go), which reads
// RemoteAddr, never X-Forwarded-For — XFF is client-spoofable and must not be
// trusted as an audit source.
func auditActor(r *http.Request) string {
	if a := strings.TrimSpace(r.Header.Get("X-Actor")); a != "" {
		return a
	}
	return "operator"
}

// recordAudit appends one entry to the audit log. Best-effort: a failure to
// write is logged but never surfaced to the caller, since the audited action
// has already happened and the audit log must not gate the control plane.
func (s *HTTPServer) recordAudit(ctx context.Context, e ledger.AuditEntry) {
	if s.ledger == nil {
		return
	}
	if _, err := s.ledger.RecordAudit(ctx, e); err != nil {
		slog.Error("record audit", "action", e.Action, "err", err)
	}
}
