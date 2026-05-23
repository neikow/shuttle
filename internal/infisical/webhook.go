// Package infisical decodes and authenticates Infisical secret-change webhooks
// so the orchestrator can redeploy the services affected by a changed secret.
package infisical

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

const (
	maxBodyBytes = 1 << 20 // 1 MiB
	// SignatureHeader is the HMAC signature Infisical sends with each webhook.
	SignatureHeader = "x-infisical-signature"
)

// Payload is the subset of an Infisical webhook body we act on. Infisical nests
// the environment and secret path under "project"; we also accept them at the
// top level for resilience across versions.
type Payload struct {
	Event   string `json:"event"`
	Project struct {
		WorkspaceID string `json:"workspaceId"`
		Environment string `json:"environment"`
		SecretPath  string `json:"secretPath"`
	} `json:"project"`
	Environment string `json:"environment"`
	SecretPath  string `json:"secretPath"`
}

// Env returns the environment slug the change occurred in (nested wins).
func (p *Payload) Env() string {
	if p.Project.Environment != "" {
		return p.Project.Environment
	}
	return p.Environment
}

// Path returns the secret folder path that changed (nested wins, default "/").
func (p *Payload) Path() string {
	switch {
	case p.Project.SecretPath != "":
		return p.Project.SecretPath
	case p.SecretPath != "":
		return p.SecretPath
	default:
		return "/"
	}
}

// Handler authenticates and decodes Infisical webhooks. When secret is empty
// signature verification is skipped (Infisical webhooks may be unsigned), but
// callers are expected to require a secret in production.
type Handler struct {
	secret string
}

func NewHandler(secret string) *Handler { return &Handler{secret: secret} }

// Parse reads the body, verifies the Infisical HMAC signature (when a secret is
// configured), and decodes the payload.
//
// Test pings sent from the Infisical dashboard use event "test" and arrive
// without a signature. Parse accepts them unsigned so the endpoint can respond
// 200 without erroring; the handler must short-circuit on event == "test".
// A present-but-wrong signature is still rejected even for test events.
func (h *Handler) Parse(r *http.Request) (*Payload, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if int64(len(body)) > maxBodyBytes {
		return nil, fmt.Errorf("body too large")
	}

	slog.Debug("infisical parse: raw body", "body", string(body), "secret_configured", h.secret != "")

	var p Payload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}

	sigHeader := r.Header.Get(SignatureHeader)
	slog.Debug("infisical parse: decoded", "event", p.Event, "sig_header", sigHeader)

	if h.secret != "" {
		// Unsigned test pings are accepted without a signature; everything else
		// requires a valid HMAC.
		if p.Event != "test" || sigHeader != "" {
			slog.Debug("infisical parse: verifying signature")
			if err := VerifySignature(body, h.secret, sigHeader); err != nil {
				slog.Debug("infisical parse: signature verification failed", "err", err)
				return nil, fmt.Errorf("signature: %w", err)
			}
			slog.Debug("infisical parse: signature ok")
		} else {
			slog.Debug("infisical parse: skipping signature (unsigned test ping)")
		}
	}

	return &p, nil
}

// VerifySignature checks Infisical's x-infisical-signature header, formatted as
// "t=<timestamp>;<hex-hmac>". The signed message is the raw request body;
// the timestamp is present for replay detection but is not part of the HMAC.
func VerifySignature(body []byte, secret, header string) error {
	ts, sig := parseSignatureHeader(header)
	if ts == "" || sig == "" {
		return fmt.Errorf("malformed signature header")
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return fmt.Errorf("invalid signature hex: %w", err)
	}
	want := computeMAC(body, secret)
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

// ComputeHeader builds the signature header value for a timestamp + body, used
// by tests and clients.
func ComputeHeader(ts string, body []byte, secret string) string {
	return fmt.Sprintf("t=%s;%s", ts, hex.EncodeToString(computeMAC(body, secret)))
}

func parseSignatureHeader(header string) (ts, sig string) {
	// Accept both "," and ";" as field separators.
	// Infisical sends either "t=<ts>,v1=<hex>" or "t=<ts>;<hex>" (bare hex,
	// no "v1=" prefix), so a field with no "=" is treated as the signature.
	fields := strings.FieldsFunc(header, func(r rune) bool { return r == ',' || r == ';' })
	for _, f := range fields {
		k, v, ok := strings.Cut(strings.TrimSpace(f), "=")
		if !ok {
			sig = k // bare hex value
			continue
		}
		switch k {
		case "t":
			ts = v
		case "v1":
			sig = v
		}
	}
	return ts, sig
}

func computeMAC(body []byte, secret string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return mac.Sum(nil)
}
