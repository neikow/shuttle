package orchestrator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/secrets"
)

func TestCheckService_allPresent(t *testing.T) {
	sec := secrets.NewFake(nil)
	sec.SetScope(secrets.Scope{Env: "prod", Path: "/shared"}, map[string]string{"COMMON": "c"})
	sec.SetScope(secrets.Scope{Env: "prod", Path: "/services/api"}, map[string]string{"API_KEY": "k"})

	syncer := newSecretsSyncer(t, sec, "/shared", "/services/{service}")
	sc := syncer.checkService(context.Background(), config.Service{
		Name: "api", EnvFrom: "prod", Env: map[string]string{"COMMON": "", "API_KEY": ""},
	})
	if !sc.OK() {
		t.Fatalf("want OK, got missing=%v err=%v", sc.MissingKeys, sc.Err)
	}
	if sc.BasePath != "/shared" || sc.ServicePath != "/services/api" {
		t.Fatalf("paths = %q + %q, want /shared + /services/api", sc.BasePath, sc.ServicePath)
	}
}

func TestCheckDNSCredentials(t *testing.T) {
	repo := ovhRepo(
		[]config.Service{{Name: "app", Host: "web1", Domains: []string{"app.example.com"}}},
		[]config.DNSCertificate{{Name: "star", Domains: []string{"*.example.com"}, Provider: "ovh"}},
	)

	// All creds present -> OK.
	g := &GitSyncer{secrets: fakeOVHSecrets()}
	results := g.CheckDNSCredentials(context.Background(), repo)
	if len(results) != 1 || results[0].Err != "" {
		t.Fatalf("want one passing provider, got %+v", results)
	}

	// Missing a cred -> error reported.
	g = &GitSyncer{secrets: secrets.NewFake(map[string]string{"OVH_APP_KEY": "ak"})}
	results = g.CheckDNSCredentials(context.Background(), repo)
	if len(results) != 1 || results[0].Err == "" {
		t.Fatalf("want a failing provider, got %+v", results)
	}

	// No dns.yml -> no results.
	if r := g.CheckDNSCredentials(context.Background(), &config.Repo{}); r != nil {
		t.Errorf("no DNS config => nil, got %+v", r)
	}
}

func TestCheckService_reportsMissingKeys(t *testing.T) {
	sec := secrets.NewFake(nil)
	sec.SetScope(secrets.Scope{Env: "prod", Path: "/shared"}, map[string]string{"API_KEY": "k"})

	syncer := newSecretsSyncer(t, sec, "/shared", "")
	sc := syncer.checkService(context.Background(), config.Service{
		Name: "api", EnvFrom: "prod", Env: map[string]string{"API_KEY": "", "DB_URL": "", "MISSING": ""},
	})
	if sc.OK() {
		t.Fatal("want failure for missing keys")
	}
	if len(sc.MissingKeys) != 2 || sc.MissingKeys[0] != "secret:DB_URL" || sc.MissingKeys[1] != "secret:MISSING" {
		t.Fatalf("missing = %v, want [secret:DB_URL secret:MISSING]", sc.MissingKeys)
	}
}

// A provider-sourced env value with no provider configured is a real
// misconfiguration — the deploy would fail — so check reports it.
func TestCheckService_providerRefNoProviderErrors(t *testing.T) {
	syncer := newSecretsSyncer(t, nil, "/shared", "")
	sc := syncer.checkService(context.Background(), config.Service{
		Name: "api", EnvFrom: "prod", Env: map[string]string{"WHATEVER": ""},
	})
	if sc.OK() || sc.Err == "" {
		t.Fatalf("want error when env references a provider that isn't configured, got %+v", sc)
	}
}

// Literal / ${env:} values need no provider, so a service using only those
// passes even with no provider configured.
func TestCheckService_literalEnvNoProvider(t *testing.T) {
	t.Setenv("REGION", "eu")
	syncer := newSecretsSyncer(t, nil, "/shared", "")
	sc := syncer.checkService(context.Background(), config.Service{
		Name: "api", Env: map[string]string{"LOG": "info", "R": "${env:REGION}"},
	})
	if !sc.OK() {
		t.Fatalf("literal/process-env values should pass with no provider, got %+v", sc)
	}
	if sc.BasePath != "" {
		t.Errorf("no provider fetch expected, got base path %q", sc.BasePath)
	}
}

func TestCheckService_noEnvSkips(t *testing.T) {
	sec := secrets.NewFake(nil)
	syncer := newSecretsSyncer(t, sec, "/shared", "")
	sc := syncer.checkService(context.Background(), config.Service{Name: "api", EnvFrom: "prod"})
	if !sc.OK() {
		t.Fatalf("no env should pass, got %+v", sc)
	}
}

func TestCheckReport_OK(t *testing.T) {
	r := &CheckReport{Services: []ServiceCheck{
		{Service: "a"},
		{Service: "b", MissingKeys: []string{"X"}},
	}}
	if r.OK() {
		t.Fatal("report with a failing service should not be OK")
	}
	r.Services = r.Services[:1]
	if !r.OK() {
		t.Fatal("report with only passing services should be OK")
	}
}

// The /check endpoint serializes the report as JSON, so errors must survive the
// round-trip (an `error` field would have marshaled to {}). Guards the remote
// `shuttle check --url` path.
func TestCheckReport_JSONRoundTrip(t *testing.T) {
	want := &CheckReport{
		SHA:         "abcdef123456",
		HasProvider: true,
		GitCredentials: []GitCredentialCheckResult{
			{RepoPrefix: "github.com/acme", Key: "GH_TOKEN", Err: "missing key"},
		},
		Services: []ServiceCheck{
			{Service: "api", Env: "prod", MissingKeys: []string{"DB_URL"}},
			{Service: "web", Err: `secrets (base "/shared"): boom`, Warnings: []string{"cannot scale"}},
		},
	}
	blob, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got CheckReport
	if err := json.Unmarshal(blob, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Services[1].Err != want.Services[1].Err {
		t.Fatalf("service err = %q, want %q", got.Services[1].Err, want.Services[1].Err)
	}
	if got.GitCredentials[0].Err != want.GitCredentials[0].Err {
		t.Fatalf("cred err = %q, want %q", got.GitCredentials[0].Err, want.GitCredentials[0].Err)
	}
	if got.OK() {
		t.Fatal("report with failures should not be OK after round-trip")
	}
}
