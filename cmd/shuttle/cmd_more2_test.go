package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRunCheckAndPlanRemote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/check":
			_, _ = w.Write([]byte(`{"sha":"abc","has_provider":true}`))
		case "/plan":
			_, _ = w.Write([]byte(`{"sha":"abc","services":[]}`))
		}
	}))
	defer srv.Close()
	base := map[string]string{"url": srv.URL, "token": "t"}

	if err := checkCmd.RunE(flagCmd(base), nil); err != nil {
		t.Errorf("runCheck: %v", err)
	}
	if err := planCmd.RunE(flagCmd(base), nil); err != nil {
		t.Errorf("runPlan: %v", err)
	}
}

func TestRunDoctor(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yml")
	body := "bearer_token: secret\ndata_dir: " + filepath.Join(dir, "data") + "\nsecrets_provider: none\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// runDoctor runs config parse + git/docker/datadir/transport/secrets probes
	// against the real host; with no repo_url/TLS those are skips/warns, so it
	// exits without a hard failure.
	if err := doctorCmd.RunE(flagCmd(map[string]string{"config": cfg}), nil); err != nil {
		t.Errorf("runDoctor: %v", err)
	}
}
