package orchestrator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWhoami proves GET /whoami reports the resolved identity (name + role) the
// UI gates its mutation screens on: the static bootstrap bearer → admin with no
// name; named control tokens → their name + role; a missing/invalid token → 401.
func TestWhoami(t *testing.T) {
	srv := newHTTPTestServer(t)

	readTok := mintToken(t, srv, "id-r", "reader", "read")
	deployTok := mintToken(t, srv, "id-d", "deployer", "deploy")
	adminTok := mintToken(t, srv, "id-a", "boss", "admin")

	cases := []struct {
		name, tok  string
		wantName   string
		wantRole   string
		wantStatus int
	}{
		{"static bearer", testToken, "", "admin", http.StatusOK},
		{"read token", readTok, "reader", "read", http.StatusOK},
		{"deploy token", deployTok, "deployer", "deploy", http.StatusOK},
		{"admin token", adminTok, "boss", "admin", http.StatusOK},
		{"no token", "", "", "", http.StatusUnauthorized},
		{"bad token", "garbage", "", "", http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, bearerReq(http.MethodGet, "/whoami", c.tok))
			if w.Code != c.wantStatus {
				t.Fatalf("code = %d, want %d (%s)", w.Code, c.wantStatus, w.Body.String())
			}
			if c.wantStatus != http.StatusOK {
				return
			}
			var got struct {
				Name string `json:"name"`
				Role string `json:"role"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v (%s)", err, w.Body.String())
			}
			if got.Name != c.wantName || got.Role != c.wantRole {
				t.Errorf("got {name:%q role:%q}, want {name:%q role:%q}", got.Name, got.Role, c.wantName, c.wantRole)
			}
		})
	}
}
