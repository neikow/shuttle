package orchestrator

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadyz(t *testing.T) {
	s := NewHTTPServer("tok", nil, nil)

	// Before SetReady: 503.
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("readyz before ready = %d, want 503", rec.Code)
	}

	// healthz is liveness — always 200 regardless of readiness.
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("healthz = %d, want 200", rec.Code)
	}

	// After SetReady(true): 200.
	s.SetReady(true)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("readyz after ready = %d, want 200", rec.Code)
	}

	// Draining flips it back to 503.
	s.SetReady(false)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("readyz while draining = %d, want 503", rec.Code)
	}
}

// readyz/healthz must be reachable without a bearer token (load balancers poll
// them tokenless).
func TestReadyzAndHealthzUnauthenticated(t *testing.T) {
	s := NewHTTPServer("tok", nil, nil)
	s.SetReady(true)
	for _, path := range []string{"/readyz", "/healthz"} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s without token = %d, want 200", path, rec.Code)
		}
	}
}
