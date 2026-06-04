// Package infisical decodes and authenticates Infisical secret-change webhooks
// so the orchestrator can redeploy the services affected by a changed secret.
package infisical

import (
	"context"
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
	"time"
)

const (
	maxBodyBytes = 1 << 20 // 1 MiB
	// nonceTTL bounds how long a signed webhook body is remembered for replay
	// rejection. Matches the repo webhook's window.
	nonceTTL = 10 * time.Minute
	// SignatureHeader is the HMAC signature Infisical sends with each webhook.
	SignatureHeader = "x-infisical-signature"

	// Infisical webhook event types.
	//
	// EventSecretsModified fires when secrets in the configured scope are
	// created, updated, or deleted. This is the primary trigger for redeploy.
	//
	// EventSecretsRotationFailed fires when a secret rotation fails. We log it
	// but do not redeploy — rotation failure does not change the live secret
	// values.
	//
	// EventTest is the connectivity ping sent from the Infisical dashboard.
	EventSecretsModified       = "secrets.modified"
	EventSecretsRotationFailed = "secrets.rotation-failed"
	EventTest                  = "test"
)

// Payload is the subset of an Infisical webhook body we act on.
type Payload struct {
	Event   string `json:"event"`
	Project struct {
		WorkspaceID        string `json:"workspaceId"`
		Environment        string `json:"environment"`
		SecretPath         string `json:"secretPath"`
		ChangedBy          string `json:"changedBy"`
		ChangedByActorType string `json:"changedByActorType"`
		// rotation-failed only
		RotationName      string `json:"rotationName"`
		ErrorMessage      string `json:"errorMessage"`
		TriggeredManually bool   `json:"triggeredManually"`
	} `json:"project"`
	// Timestamp arrives as a JSON number (epoch ms) in current payloads and as a
	// string in older ones; RawMessage tolerates both. Unused beyond decode.
	Timestamp json.RawMessage `json:"timestamp"`
	// top-level fallbacks (older payload format)
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

// NonceStore records replay-prevention nonces with a TTL, returning true if the
// nonce was already seen. Satisfied by the ledger store.
type NonceStore interface {
	SeenNonce(ctx context.Context, nonce string, ttl time.Duration) (bool, error)
}

// Handler authenticates and decodes Infisical webhooks. When secret is empty
// signature verification is skipped (Infisical webhooks may be unsigned), but
// callers are expected to require a secret in production.
type Handler struct {
	secret string
	nonces NonceStore // optional; when set, signed webhooks are replay-guarded
}

func NewHandler(secret string) *Handler { return &Handler{secret: secret} }

// SetNonceStore attaches a replay-prevention store. Once set, a signed webhook
// whose body was already seen within the TTL is rejected. Call before serving.
func (h *Handler) SetNonceStore(n NonceStore) { h.nonces = n }

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

	slog.Debug("infisical parse: read body", "bytes", len(body), "secret_configured", h.secret != "")

	var p Payload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}

	sigHeader := r.Header.Get(SignatureHeader)
	slog.Debug("infisical parse: decoded", "event", p.Event, "sig_header", sigHeader)

	if h.secret != "" {
		// Unsigned test pings are accepted without a signature; everything else
		// requires a valid HMAC.
		if p.Event != EventTest || sigHeader != "" {
			slog.Debug("infisical parse: verifying signature")
			if err := VerifySignature(body, h.secret, sigHeader); err != nil {
				slog.Debug("infisical parse: signature verification failed", "err", err)
				return nil, fmt.Errorf("signature: %w", err)
			}
			slog.Debug("infisical parse: signature ok")
		} else {
			slog.Debug("infisical parse: skipping signature (unsigned test ping)")
		}

		// Replay guard: the signature covers only the body, so a captured signed
		// webhook could otherwise be replayed indefinitely (the t= timestamp is
		// not part of the HMAC and is attacker-controlled). Reject a body already
		// seen within the TTL. Skipped for unsigned test pings.
		if h.nonces != nil && p.Event != EventTest {
			seen, err := h.nonces.SeenNonce(r.Context(), bodyNonce(body), nonceTTL)
			if err != nil {
				return nil, fmt.Errorf("nonce check: %w", err)
			}
			if seen {
				return nil, fmt.Errorf("replay detected")
			}
		}
	}

	return &p, nil
}

// bodyNonce derives a replay nonce from the request body. The "infisical/"
// domain prefix separates these nonces from the repo webhook's (both share the
// ledger's nonce table).
func bodyNonce(body []byte) string {
	h := sha256.New()
	h.Write([]byte("infisical/"))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil)[:16])
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
