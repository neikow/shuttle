package orchestrator

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSecureHeaders asserts the baseline security headers ride on every
// response, and that the CSP is attached to /ui paths.
func TestSecureHeaders(t *testing.T) {
	srv := newHTTPTestServer(t)

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	for k, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := w.Header().Get(k); got != want {
			t.Errorf("/healthz header %s = %q, want %q", k, got, want)
		}
	}
	if csp := w.Header().Get("Content-Security-Policy"); csp != "" {
		t.Errorf("/healthz should not carry CSP, got %q", csp)
	}

	// /ui paths carry the CSP (even a 404 in a non-embedui build, since the
	// header is set before the mux runs).
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ui/", nil))
	if csp := w.Header().Get("Content-Security-Policy"); csp != uiCSP {
		t.Errorf("/ui/ CSP = %q, want uiCSP", csp)
	}
}

// TestMetricsAuth covers both modes of EnableMetrics.
func TestMetricsAuth(t *testing.T) {
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "shuttle_events_total 1")
	})

	t.Run("unauthed default", func(t *testing.T) {
		srv := newHTTPTestServer(t)
		srv.EnableMetrics(stub, false)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("no-token /metrics (unauthed) = %d, want 200", w.Code)
		}
	})

	t.Run("require auth", func(t *testing.T) {
		srv := newHTTPTestServer(t)
		srv.EnableMetrics(stub, true)

		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("no-token /metrics (auth) = %d, want 401", w.Code)
		}

		w = httptest.NewRecorder()
		srv.ServeHTTP(w, bearerReq(http.MethodGet, "/metrics", testToken))
		if w.Code != http.StatusOK {
			t.Fatalf("bearer /metrics (auth) = %d, want 200 (%s)", w.Code, w.Body.String())
		}
	})
}

// TestRedeemRateLimited proves POST /enroll/redeem is behind the per-IP limiter:
// a burst from one IP eventually trips 429 (the limiter runs before the handler,
// so a bad/empty body never reaches it once the bucket is drained).
func TestRedeemRateLimited(t *testing.T) {
	srv := newHTTPTestServer(t)
	srv.EnableEnrollment(EnrollOptions{})

	saw429 := false
	for range 60 {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/enroll/redeem", nil))
		if w.Code == http.StatusTooManyRequests {
			saw429 = true
			break
		}
	}
	if !saw429 {
		t.Fatal("expected POST /enroll/redeem to be rate limited (429), never saw it")
	}
}
