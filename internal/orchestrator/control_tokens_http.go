package orchestrator

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/neikow/shuttle/internal/ledger"
	"github.com/neikow/shuttle/internal/token"
)

type createControlTokenRequest struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

// createControlTokenResponse returns the freshly minted token in plaintext —
// the only time it is ever shown, mirroring `shuttle enroll`. Only its hash is
// stored.
type createControlTokenResponse struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Role  string `json:"role"`
	Token string `json:"token"`
}

func (s *HTTPServer) handleCreateControlToken(w http.ResponseWriter, r *http.Request) {
	var req createControlTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	role, err := ParseRole(req.Role)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	tok, err := token.Generate()
	if err != nil {
		http.Error(w, "generate token", http.StatusInternalServerError)
		return
	}
	id := newID()
	if err := s.ledger.CreateControlToken(r.Context(), id, req.Name, token.Hash(tok), string(role)); err != nil {
		http.Error(w, "store control token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.recordAudit(r.Context(), ledger.AuditEntry{
		Actor: auditActor(r), Action: auditTokenCreate, Target: req.Name, SourceIP: clientIP(r),
		Result: auditSuccess, Detail: "role=" + string(role) + " token_id=" + id,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(createControlTokenResponse{
		ID: id, Name: req.Name, Role: string(role), Token: tok,
	})
}

func (s *HTTPServer) handleListControlTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.ledger.ListControlTokens(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if tokens == nil {
		tokens = []ledger.ControlToken{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tokens)
}

func (s *HTTPServer) handleRevokeControlToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.ledger.RevokeControlToken(r.Context(), id); err != nil {
		if errors.As(err, new(ledger.ErrControlTokenNotFound)) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAudit(r.Context(), ledger.AuditEntry{
		Actor: auditActor(r), Action: auditTokenRevoke, Target: id, SourceIP: clientIP(r),
		Result: auditSuccess,
	})
	w.WriteHeader(http.StatusNoContent)
}
