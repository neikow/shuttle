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
		CAPEM:         "-----BEGIN CERTIFICATE-----\nFAKE\n-----END CERTIFICATE-----\n",
		Hosts: func(context.Context) ([]config.Host, error) {
			return []config.Host{{Name: "web1"}, {Name: "web2"}}, nil
		},
	})
	return srv
}

// mintJoinToken runs the bearer-authed /enroll step and returns the join token.
func mintJoinToken(t *testing.T, srv *HTTPServer, host string) enrollResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/enroll", strings.NewReader(`{"host":"`+host+`"}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("enroll want 201, got %d: %s", w.Code, w.Body)
	}
	var res enrollResponse
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode enroll: %v", err)
	}
	return res
}

func redeem(t *testing.T, srv *HTTPServer, joinToken string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(redeemRequest{JoinToken: joinToken})
	req := httptest.NewRequest(http.MethodPost, "/enroll/redeem", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
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

func TestEnroll_mintsJoinToken(t *testing.T) {
	srv := newEnrollServer(t)
	res := mintJoinToken(t, srv, "web1")
	if res.JoinToken == "" || res.ID == "" {
		t.Fatalf("missing join token/id: %+v", res)
	}
	if res.ExpiresAtUMS == 0 {
		t.Fatalf("missing expiry: %+v", res)
	}
	// The join token is not yet an agent credential.
	if _, ok, _ := srv.ledger.AgentTokenHost(context.Background(), token.Hash(res.JoinToken)); ok {
		t.Fatal("join token must not register as an agent token before redeem")
	}
}

func TestRedeem_success(t *testing.T) {
	srv := newEnrollServer(t)
	join := mintJoinToken(t, srv, "web1")

	w := redeem(t, srv, join.JoinToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("redeem want 201, got %d: %s", w.Code, w.Body)
	}
	var res redeemResponse
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode redeem: %v", err)
	}
	if res.Token == "" || res.Host != "web1" {
		t.Fatalf("bad redeem response: %+v", res)
	}
	if res.GRPCAddr != "orch.example.com:9090" || res.ServerName != "orchestrator" || !res.TLS {
		t.Fatalf("missing connection info: %+v", res)
	}
	if !strings.Contains(res.CAPEM, "BEGIN CERTIFICATE") {
		t.Fatalf("ca_pem not handed back: %q", res.CAPEM)
	}
	// The redeemed token must now be a valid agent credential for web1.
	host, ok, err := srv.ledger.AgentTokenHost(context.Background(), token.Hash(res.Token))
	if err != nil || !ok || host != "web1" {
		t.Fatalf("agent token not stored for web1: host=%q ok=%v err=%v", host, ok, err)
	}
}

func TestRedeem_singleUse(t *testing.T) {
	srv := newEnrollServer(t)
	join := mintJoinToken(t, srv, "web1")

	if w := redeem(t, srv, join.JoinToken); w.Code != http.StatusCreated {
		t.Fatalf("first redeem want 201, got %d", w.Code)
	}
	if w := redeem(t, srv, join.JoinToken); w.Code != http.StatusUnauthorized {
		t.Fatalf("second redeem want 401, got %d: %s", w.Code, w.Body)
	}
}

func TestRedeem_invalidToken(t *testing.T) {
	srv := newEnrollServer(t)
	if w := redeem(t, srv, "not-a-real-token"); w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
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
