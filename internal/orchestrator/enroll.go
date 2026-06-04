package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/ledger"
	"github.com/neikow/shuttle/internal/token"
)

// defaultJoinTTL bounds how long a minted join token stays redeemable. Short by
// design: it is a one-time bootstrap credential carried to the target host.
const defaultJoinTTL = 15 * time.Minute

// EnrollOptions configures the agent-enrollment endpoints.
type EnrollOptions struct {
	// AdvertiseAddr is the gRPC host:port handed back to a redeeming agent.
	AdvertiseAddr string
	// ServerName is the orchestrator cert SAN handed back to a redeeming agent.
	ServerName string
	// TLS indicates the gRPC transport uses TLS.
	TLS bool
	// CAPEM is the PEM the agent should trust for the gRPC connection (the gRPC
	// CA, or the self-signed server cert acting as its own root). Handed to the
	// agent at redeem time so no CA file has to be distributed out-of-band.
	CAPEM string
	// JoinTTL overrides defaultJoinTTL when non-zero.
	JoinTTL time.Duration
	// Hosts lists the managed hosts an agent may enroll as (from the IaC repo).
	Hosts func(ctx context.Context) ([]config.Host, error)
}

func (o *EnrollOptions) ttl() time.Duration {
	if o.JoinTTL > 0 {
		return o.JoinTTL
	}
	return defaultJoinTTL
}

// EnableEnrollment registers the enrollment endpoints. GET /hosts and
// POST /enroll are bearer-authed (operator side). POST /enroll/redeem is
// authenticated by the single-use join token itself (agent side, no bearer).
// Call before serving; requires the ledger store to be set.
func (s *HTTPServer) EnableEnrollment(opts EnrollOptions) {
	s.enroll = &opts
	s.mux.HandleFunc("GET /hosts", s.bearerAuth(s.handleListHosts))
	s.mux.HandleFunc("POST /enroll", s.bearerAuth(s.handleEnroll))
	s.mux.HandleFunc("POST /enroll/redeem", s.handleRedeem)
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

// enrollResponse is the bearer-authed /enroll result: a short-lived single-use
// join token bound to the host. The operator's `shuttle enroll` turns this into
// the `shuttle agent join` one-liner (adding the cert pin it computed locally).
type enrollResponse struct {
	ID           string `json:"id"`
	Host         string `json:"host"`
	JoinToken    string `json:"join_token"`
	ExpiresAtUMS int64  `json:"expires_at_unix_ms"`
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

	// Opportunistically drop expired tokens so the table can't grow unbounded;
	// failure here is non-fatal to the mint.
	if _, perr := s.ledger.PurgeExpiredJoinTokens(r.Context(), time.Now()); perr != nil {
		slog.Warn("purge expired join tokens", "err", perr)
	}

	join, err := token.Generate()
	if err != nil {
		http.Error(w, "generate token", http.StatusInternalServerError)
		return
	}
	id := newID()
	exp := time.Now().Add(s.enroll.ttl())
	if err := s.ledger.CreateJoinToken(r.Context(), id, req.Host, token.Hash(join), exp); err != nil {
		http.Error(w, "store join token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	slog.Info("join token minted", "id", id, "host", req.Host, "expires_at", exp)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(enrollResponse{
		ID:           id,
		Host:         req.Host,
		JoinToken:    join,
		ExpiresAtUMS: exp.UnixMilli(),
	})
}

type redeemRequest struct {
	JoinToken string `json:"join_token"`
}

// redeemResponse hands the redeeming agent everything it needs to connect: the
// long-lived host-scoped token, the gRPC address/SAN, and the CA to trust — so
// no CA file is distributed separately.
type redeemResponse struct {
	Token      string `json:"token"`
	Host       string `json:"host"`
	GRPCAddr   string `json:"grpc_addr"`
	ServerName string `json:"server_name,omitempty"`
	TLS        bool   `json:"tls"`
	CAPEM      string `json:"ca_pem,omitempty"`
}

func (s *HTTPServer) handleRedeem(w http.ResponseWriter, r *http.Request) {
	var req redeemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.JoinToken == "" {
		http.Error(w, "join_token required", http.StatusBadRequest)
		return
	}

	host, err := s.ledger.RedeemJoinToken(r.Context(), token.Hash(req.JoinToken), time.Now())
	if errors.Is(err, ledger.ErrJoinTokenInvalid) {
		// Undifferentiated 401: unknown, expired, and already-used all look alike.
		http.Error(w, "join token invalid, expired, or already used", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "redeem: "+err.Error(), http.StatusInternalServerError)
		return
	}

	agentTok, err := token.Generate()
	if err != nil {
		http.Error(w, "generate token", http.StatusInternalServerError)
		return
	}
	id := newID()
	if err := s.ledger.CreateAgentToken(r.Context(), id, host, token.Hash(agentTok)); err != nil {
		http.Error(w, "store agent token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	slog.Info("join token redeemed", "host", host, "agent_token_id", id)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(redeemResponse{
		Token:      agentTok,
		Host:       host,
		GRPCAddr:   s.enroll.AdvertiseAddr,
		ServerName: s.enroll.ServerName,
		TLS:        s.enroll.TLS,
		CAPEM:      s.enroll.CAPEM,
	})
}

func hostExists(hosts []config.Host, name string) bool {
	for _, h := range hosts {
		if h.Name == name {
			return true
		}
	}
	return false
}
