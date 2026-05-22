package webhook

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeNonces struct {
	seen map[string]bool
}

func (f *fakeNonces) SeenNonce(_ context.Context, nonce string, _ time.Duration) (bool, error) {
	if f.seen[nonce] {
		return true, nil
	}
	if f.seen == nil {
		f.seen = make(map[string]bool)
	}
	f.seen[nonce] = true
	return false, nil
}

const testSecret = "test-secret"

func buildRequest(t *testing.T, body string, secret string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(body))
	req.Header.Set("X-Hub-Signature-256", ComputeHeader([]byte(body), secret))
	req.Header.Set("X-Shuttle-Timestamp", "1234567890")
	return req
}

func TestParse_valid(t *testing.T) {
	h := NewHandler(testSecret, &fakeNonces{})
	body := `{"ref":"refs/heads/main","commit_sha":"abc","repo":"org/repo","services":["app"]}`
	p, err := h.Parse(buildRequest(t, body, testSecret))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.CommitSHA != "abc" {
		t.Errorf("want abc, got %s", p.CommitSHA)
	}
}

func TestParse_badSignature(t *testing.T) {
	h := NewHandler(testSecret, &fakeNonces{})
	body := `{"ref":"main","commit_sha":"abc","repo":"r"}`
	_, err := h.Parse(buildRequest(t, body, "wrong-secret"))
	if err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("expected signature error, got %v", err)
	}
}

func TestParse_replay(t *testing.T) {
	h := NewHandler(testSecret, &fakeNonces{})
	body := `{"ref":"main","commit_sha":"abc","repo":"r"}`
	req1 := buildRequest(t, body, testSecret)
	req2 := buildRequest(t, body, testSecret)
	// Same body + timestamp = same nonce.
	req2.Header.Set("X-Shuttle-Timestamp", req1.Header.Get("X-Shuttle-Timestamp"))

	if _, err := h.Parse(req1); err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	_, err := h.Parse(req2)
	if err == nil || !strings.Contains(err.Error(), "replay") {
		t.Fatalf("expected replay error, got %v", err)
	}
}

func TestParse_missingSignature(t *testing.T) {
	h := NewHandler(testSecret, &fakeNonces{})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString("{}"))
	req.Header.Set("X-Shuttle-Timestamp", "1234567890")
	_, err := h.Parse(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestParse_oversizedBody(t *testing.T) {
	h := NewHandler(testSecret, &fakeNonces{})
	large := strings.Repeat("x", maxBodyBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(large))
	req.Header.Set("X-Hub-Signature-256", ComputeHeader([]byte(large), testSecret))
	req.Header.Set("X-Shuttle-Timestamp", "1234567890")
	_, err := h.Parse(req)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected too-large error, got %v", err)
	}
}
