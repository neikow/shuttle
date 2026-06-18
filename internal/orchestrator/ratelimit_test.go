package orchestrator

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIPRateLimiterBurstThenDeny(t *testing.T) {
	// 60/min = 1/s, burst 3: the first 3 immediate requests pass, the 4th is
	// denied (no meaningful refill within microseconds).
	l := newIPRateLimiter(60, 3)
	for i := range 3 {
		if !l.allow("1.2.3.4") {
			t.Fatalf("request %d should be allowed (within burst)", i+1)
		}
	}
	if l.allow("1.2.3.4") {
		t.Error("4th request should be denied (burst exhausted)")
	}
}

func TestIPRateLimiterPerIPIsolation(t *testing.T) {
	l := newIPRateLimiter(60, 1)
	if !l.allow("1.1.1.1") {
		t.Fatal("first IP first request should pass")
	}
	if l.allow("1.1.1.1") {
		t.Fatal("first IP second request should be denied")
	}
	// A different IP has its own bucket and is unaffected.
	if !l.allow("2.2.2.2") {
		t.Error("second IP should have an independent bucket")
	}
}

func TestIPRateLimiterMiddleware429(t *testing.T) {
	l := newIPRateLimiter(60, 1)
	var hits int
	h := l.middleware(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	})

	rec1 := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
	req.RemoteAddr = "9.9.9.9:5555"
	h(rec1, req)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first call = %d, want 200", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	h(rec2, req)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("second call = %d, want 429", rec2.Code)
	}
	if rec2.Header().Get("Retry-After") == "" {
		t.Error("429 should carry a Retry-After header")
	}
	if hits != 1 {
		t.Errorf("handler ran %d times, want 1 (the 429 must short-circuit)", hits)
	}
}

func TestClientIP(t *testing.T) {
	tests := map[string]string{
		"1.2.3.4:5678": "1.2.3.4",
		"[::1]:9090":   "::1",
		"garbage":      "garbage",
	}
	for remote, want := range tests {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = remote
		if got := clientIP(req); got != want {
			t.Errorf("clientIP(%q) = %q, want %q", remote, got, want)
		}
	}
}

func TestSetWebhookRateLimit(t *testing.T) {
	s := NewHTTPServer("tok", nil, nil)
	if s.webhookLimiter == nil {
		t.Fatal("default should have a limiter")
	}
	s.SetWebhookRateLimit(-1)
	if s.webhookLimiter != nil {
		t.Error("negative value should disable the limiter")
	}
	s.SetWebhookRateLimit(600)
	if s.webhookLimiter == nil {
		t.Error("positive value should install a limiter")
	}
}
