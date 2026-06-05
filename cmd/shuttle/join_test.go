package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neikow/shuttle/internal/mtls"
)

func redeemTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /enroll/redeem", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["join_token"] != "good-join" {
			http.Error(w, "join token invalid", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(redeemResult{
			Token:      "agent-secret",
			Host:       "web-1",
			GRPCAddr:   "orch:9090",
			ServerName: "orchestrator",
			TLS:        true,
			CAPEM:      "-----BEGIN CERTIFICATE-----\nFAKE\n-----END CERTIFICATE-----\n",
		})
	})
	return httptest.NewTLSServer(mux)
}

func TestRedeemJoinToken_pinnedSuccess(t *testing.T) {
	ts := redeemTestServer(t)
	defer ts.Close()
	pin := mtls.SPKIPin(ts.Certificate())

	res, err := redeemJoinToken(context.Background(), ts.URL, "good-join", pin)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if res.Token != "agent-secret" || res.Host != "web-1" || res.GRPCAddr != "orch:9090" {
		t.Fatalf("bad result: %+v", res)
	}
}

func TestRedeemJoinToken_pinMismatch(t *testing.T) {
	ts := redeemTestServer(t)
	defer ts.Close()

	_, err := redeemJoinToken(context.Background(), ts.URL, "good-join", "sha256:bogusbogusbogusbogusbogusbogusbogusbogus0=")
	if err == nil {
		t.Fatal("a mismatched pin must fail redeem")
	}
}

func TestRedeemJoinToken_invalidToken(t *testing.T) {
	ts := redeemTestServer(t)
	defer ts.Close()
	pin := mtls.SPKIPin(ts.Certificate())

	if _, err := redeemJoinToken(context.Background(), ts.URL, "wrong", pin); err == nil {
		t.Fatal("an invalid join token must fail redeem")
	}
}

func TestPersistCredentials(t *testing.T) {
	dir := t.TempDir()
	caFile, err := persistCredentials(dir, &redeemResult{Token: "tok", CAPEM: "PEMDATA"})
	if err != nil {
		t.Fatalf("persist: %v", err)
	}

	tokBytes, err := os.ReadFile(filepath.Join(dir, agentTokenFile))
	if err != nil || string(tokBytes) != "tok" {
		t.Fatalf("token file: %q err=%v", tokBytes, err)
	}
	info, err := os.Stat(filepath.Join(dir, agentTokenFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token perms = %v err=%v, want 0600", info.Mode().Perm(), err)
	}
	if caFile != filepath.Join(dir, orchestratorCA) {
		t.Fatalf("ca path = %q", caFile)
	}
	caBytes, _ := os.ReadFile(caFile)
	if string(caBytes) != "PEMDATA" {
		t.Fatalf("ca file = %q", caBytes)
	}
}

func TestPersistCredentials_noCA(t *testing.T) {
	dir := t.TempDir()
	caFile, err := persistCredentials(dir, &redeemResult{Token: "tok"})
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if caFile != "" {
		t.Fatalf("ca file should be empty, got %q", caFile)
	}
	if _, err := os.Stat(filepath.Join(dir, orchestratorCA)); !os.IsNotExist(err) {
		t.Fatal("no CA file should be written when CAPEM is empty")
	}
}

func TestBuildJoinCommand(t *testing.T) {
	withPin := buildJoinCommand("https://orch:8080", "JT", "sha256:abc")
	for _, want := range []string{"shuttle agent join", "--redeem-url https://orch:8080", "--token JT", "--pin sha256:abc"} {
		if !strings.Contains(withPin, want) {
			t.Errorf("command %q missing %q", withPin, want)
		}
	}
	noPin := buildJoinCommand("http://orch:8080", "JT", "")
	if strings.Contains(noPin, "--pin") {
		t.Errorf("no-pin command must omit --pin: %q", noPin)
	}
}
