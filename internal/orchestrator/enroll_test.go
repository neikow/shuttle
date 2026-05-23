package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/token"
)

func newEnrollServer(t *testing.T) *HTTPServer {
	t.Helper()
	srv := newHTTPTestServer(t)
	srv.EnableEnrollment(EnrollOptions{
		AdvertiseAddr: "orch.example.com:9090",
		ServerName:    "orchestrator",
		TLS:           true,
		Hosts: func(context.Context) ([]config.Host, error) {
			return []config.Host{{Name: "web1"}, {Name: "web2"}}, nil
		},
	})
	return srv
}

func TestListHosts(t *testing.T) {
	srv := newEnrollServer(t)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodGet, "/hosts"))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var hosts []hostInfo
	if err := json.Unmarshal(w.Body.Bytes(), &hosts); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(hosts) != 2 || hosts[0].Name != "web1" {
		t.Fatalf("hosts = %+v, want web1,web2", hosts)
	}
}

func TestEnroll_success(t *testing.T) {
	srv := newEnrollServer(t)
	req := httptest.NewRequest(http.MethodPost, "/enroll", strings.NewReader(`{"host":"web1"}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}
	var res enrollResponse
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Token == "" || res.ID == "" {
		t.Fatalf("missing token/id: %+v", res)
	}
	for _, want := range []string{"shuttle agent", "--orchestrator orch.example.com:9090", "--host web1", "--token " + res.Token, "--server-name orchestrator"} {
		if !strings.Contains(res.Command, want) {
			t.Errorf("command %q missing %q", res.Command, want)
		}
	}

	// The token must be valid for the host in the ledger.
	host, ok, err := srv.ledger.AgentTokenHost(context.Background(), token.Hash(res.Token))
	if err != nil || !ok || host != "web1" {
		t.Fatalf("token not stored for web1: host=%q ok=%v err=%v", host, ok, err)
	}
}

func TestEnroll_unknownHost(t *testing.T) {
	srv := newEnrollServer(t)
	req := httptest.NewRequest(http.MethodPost, "/enroll", strings.NewReader(`{"host":"ghost"}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestEnroll_unauthorized(t *testing.T) {
	srv := newEnrollServer(t)
	req := httptest.NewRequest(http.MethodPost, "/enroll", strings.NewReader(`{"host":"web1"}`))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}
