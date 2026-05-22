package webhook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	maxBodyBytes = 1 << 20 // 1 MiB
	nonceTTL     = 10 * time.Minute
)

// NonceStore checks and records replay-prevention nonces.
type NonceStore interface {
	SeenNonce(ctx context.Context, nonce string, ttl time.Duration) (bool, error)
}

// Handler parses and validates incoming shuttle webhooks.
type Handler struct {
	secret string
	nonces NonceStore
}

func NewHandler(secret string, nonces NonceStore) *Handler {
	return &Handler{secret: secret, nonces: nonces}
}

// Parse validates the request signature, replay nonce, body size, and decodes the payload.
func (h *Handler) Parse(r *http.Request) (*Payload, error) {
	sig := r.Header.Get("X-Hub-Signature-256")
	if sig == "" {
		return nil, fmt.Errorf("missing X-Hub-Signature-256")
	}
	tsHeader := r.Header.Get("X-Shuttle-Timestamp")
	if tsHeader == "" {
		return nil, fmt.Errorf("missing X-Shuttle-Timestamp")
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if int64(len(body)) > maxBodyBytes {
		return nil, fmt.Errorf("body too large")
	}

	if err := VerifySignature(body, h.secret, sig); err != nil {
		return nil, fmt.Errorf("signature: %w", err)
	}

	// Nonce = hex(sha256(timestamp || body)[:8])
	nonce := computeNonce(tsHeader, body)
	seen, err := h.nonces.SeenNonce(r.Context(), nonce, nonceTTL)
	if err != nil {
		return nil, fmt.Errorf("nonce check: %w", err)
	}
	if seen {
		return nil, fmt.Errorf("replay detected")
	}

	var p Payload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	return &p, nil
}

func computeNonce(ts string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(ts))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil)[:8])
}
