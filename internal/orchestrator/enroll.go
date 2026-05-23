package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/token"
)

// EnrollOptions configures the agent-enrollment endpoints.
type EnrollOptions struct {
	// AdvertiseAddr is the gRPC host:port baked into the generated agent command.
	AdvertiseAddr string
	// ServerName is the orchestrator cert SAN; added to the command when set.
	ServerName string
	// TLS indicates the gRPC transport uses TLS (the response hints the agent
	// may need --ca for a private CA).
	TLS bool
	// Hosts lists the managed hosts an agent may enroll as (from the IaC repo).
	Hosts func(ctx context.Context) ([]config.Host, error)
}

// EnableEnrollment registers the bearer-authed GET /hosts and POST /enroll
// endpoints. Call before serving. Requires the ledger store to be set.
func (s *HTTPServer) EnableEnrollment(opts EnrollOptions) {
	s.enroll = &opts
	s.mux.HandleFunc("GET /hosts", s.bearerAuth(s.handleListHosts))
	s.mux.HandleFunc("POST /enroll", s.bearerAuth(s.handleEnroll))
}

type hostInfo struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
}

func (s *HTTPServer) handleListHosts(w http.ResponseWriter, r *http.Request) {
	hosts, err := s.enroll.Hosts(r.Context())
	if err != nil {
		http.Error(w, "list hosts: "+err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]hostInfo, 0, len(hosts))
	for _, h := range hosts {
		out = append(out, hostInfo{Name: h.Name, Labels: h.Labels})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

type enrollRequest struct {
	Host string `json:"host"`
}

type enrollResponse struct {
	ID      string `json:"id"`
	Host    string `json:"host"`
	Token   string `json:"token"`
	Command string `json:"command"`
	TLS     bool   `json:"tls"`
}

func (s *HTTPServer) handleEnroll(w http.ResponseWriter, r *http.Request) {
	var req enrollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.Host == "" {
		http.Error(w, "host required", http.StatusBadRequest)
		return
	}

	// The host must exist in the IaC repo.
	hosts, err := s.enroll.Hosts(r.Context())
	if err != nil {
		http.Error(w, "list hosts: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !hostExists(hosts, req.Host) {
		http.Error(w, fmt.Sprintf("unknown host %q", req.Host), http.StatusNotFound)
		return
	}

	tok, err := token.Generate()
	if err != nil {
		http.Error(w, "generate token", http.StatusInternalServerError)
		return
	}
	id := newID()
	if err := s.ledger.CreateAgentToken(r.Context(), id, req.Host, token.Hash(tok)); err != nil {
		http.Error(w, "store token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := enrollResponse{
		ID:      id,
		Host:    req.Host,
		Token:   tok,
		Command: s.buildAgentCommand(req.Host, tok),
		TLS:     s.enroll.TLS,
	}
	slog.Info("agent enrolled", "id", id, "host", req.Host)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *HTTPServer) buildAgentCommand(host, tok string) string {
	addr := s.enroll.AdvertiseAddr
	parts := []string{
		"shuttle agent",
		"--orchestrator " + addr,
		"--host " + host,
		"--token " + tok,
	}
	if s.enroll.TLS && s.enroll.ServerName != "" {
		parts = append(parts, "--server-name "+s.enroll.ServerName)
	}
	return strings.Join(parts, " ")
}

func hostExists(hosts []config.Host, name string) bool {
	for _, h := range hosts {
		if h.Name == name {
			return true
		}
	}
	return false
}
