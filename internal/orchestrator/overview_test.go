package orchestrator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleOverview(t *testing.T) {
	reg := NewRegistry()
	reg.register("web1") // connected
	tr := NewStateTracker()
	tr.Record("web1", "app", "running", "abc1234567890")
	tr.Record("web2", "legacy", "exited", "def4567890123") // offline host, known service

	s := NewHTTPServer("tok", nil, reg)
	s.SetStateTracker(tr)

	req := httptest.NewRequest(http.MethodGet, "/overview", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var ov Overview
	if err := json.Unmarshal(rec.Body.Bytes(), &ov); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(ov.Hosts) != 2 {
		t.Fatalf("hosts = %d, want 2 (%+v)", len(ov.Hosts), ov.Hosts)
	}
	// Sorted by name: web1 then web2.
	w1, w2 := ov.Hosts[0], ov.Hosts[1]
	if w1.Name != "web1" || !w1.Connected || w1.LastSeen == nil {
		t.Fatalf("web1 = %+v, want connected with last_seen", w1)
	}
	if len(w1.Services) != 1 || w1.Services[0].Service != "app" || w1.Services[0].Status != "running" {
		t.Fatalf("web1 services = %+v", w1.Services)
	}
	if w2.Name != "web2" || w2.Connected || w2.LastSeen != nil {
		t.Fatalf("web2 = %+v, want disconnected, no last_seen", w2)
	}
	if len(w2.Services) != 1 || w2.Services[0].Status != "exited" {
		t.Fatalf("web2 services = %+v", w2.Services)
	}
}

func TestHandleOverview_unauthorized(t *testing.T) {
	s := NewHTTPServer("tok", nil, NewRegistry())
	req := httptest.NewRequest(http.MethodGet, "/overview", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
