package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// opsServer answers every ops-command endpoint with a minimal 2xx JSON body.
func opsServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/audit":
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/prune":
			_, _ = w.Write([]byte(`{"pruned":[]}`))
		case r.URL.Path == "/tokens" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"id":"t1","token":"plain","name":"ci","role":"deploy"}`))
		case r.URL.Path == "/tokens" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[{"id":"t1","name":"ci","role":"deploy"}]`))
		case r.URL.Path == "/tokens/t1":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/backups":
			_, _ = w.Write([]byte(`[]`))
		default:
			// /backup/{service}, /restore, etc.
			_, _ = w.Write([]byte(`{"backup_id":"b1","operation_id":"o1","host":"web1","snapshot_id":"s1"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOpsCommands(t *testing.T) {
	srv := opsServer(t)
	base := map[string]string{"url": srv.URL, "token": "t"}

	cases := []struct {
		name string
		run  func() error
	}{
		{"audit", func() error { return auditCmd.RunE(flagCmd(base), nil) }},
		{"prune", func() error { return pruneCmd.RunE(flagCmd(withName(base, "yes", "true")), nil) }},
		{"token-create", func() error { return tokenCreateCmd.RunE(flagCmd(withName2(base, "name", "ci", "role", "deploy")), nil) }},
		{"token-list", func() error { return tokenListCmd.RunE(flagCmd(base), nil) }},
		{"token-revoke", func() error { return tokenRevokeCmd.RunE(flagCmd(base), []string{"t1"}) }},
		{"backups", func() error { return backupsCmd.RunE(flagCmd(base), nil) }},
		{"backup-service", func() error { return backupServiceCmd.RunE(flagCmd(base), []string{"web"}) }},
		{"restore-service", func() error {
			return restoreServiceCmd.RunE(flagCmd(withName(base, "backup-id", "b1")), []string{"web"})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.run(); err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
		})
	}
}

func withName(m map[string]string, k, v string) map[string]string {
	out := map[string]string{k: v}
	for kk, vv := range m {
		out[kk] = vv
	}
	return out
}

func withName2(m map[string]string, k1, v1, k2, v2 string) map[string]string {
	return withName(withName(m, k1, v1), k2, v2)
}
