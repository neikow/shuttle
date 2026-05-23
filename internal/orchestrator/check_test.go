package orchestrator

import (
	"context"
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
		Name: "api", EnvFrom: "prod", EnvSchema: []string{"COMMON", "API_KEY"},
	})
	if !sc.OK() {
		t.Fatalf("want OK, got missing=%v err=%v", sc.MissingKeys, sc.Err)
	}
	if sc.BasePath != "/shared" || sc.ServicePath != "/services/api" {
		t.Fatalf("paths = %q + %q, want /shared + /services/api", sc.BasePath, sc.ServicePath)
	}
}

func TestCheckService_reportsMissingKeys(t *testing.T) {
	sec := secrets.NewFake(nil)
	sec.SetScope(secrets.Scope{Env: "prod", Path: "/shared"}, map[string]string{"API_KEY": "k"})

	syncer := newSecretsSyncer(t, sec, "/shared", "")
	sc := syncer.checkService(context.Background(), config.Service{
		Name: "api", EnvFrom: "prod", EnvSchema: []string{"API_KEY", "DB_URL", "MISSING"},
	})
	if sc.OK() {
		t.Fatal("want failure for missing keys")
	}
	if len(sc.MissingKeys) != 2 || sc.MissingKeys[0] != "DB_URL" || sc.MissingKeys[1] != "MISSING" {
		t.Fatalf("missing = %v, want [DB_URL MISSING]", sc.MissingKeys)
	}
}

func TestCheckService_noProviderSkips(t *testing.T) {
	syncer := newSecretsSyncer(t, nil, "/shared", "")
	sc := syncer.checkService(context.Background(), config.Service{
		Name: "api", EnvFrom: "prod", EnvSchema: []string{"WHATEVER"},
	})
	if !sc.OK() || len(sc.MissingKeys) != 0 {
		t.Fatalf("no provider should pass with no checks, got %+v", sc)
	}
}

func TestCheckService_noSchemaSkips(t *testing.T) {
	sec := secrets.NewFake(nil)
	syncer := newSecretsSyncer(t, sec, "/shared", "")
	sc := syncer.checkService(context.Background(), config.Service{Name: "api", EnvFrom: "prod"})
	if !sc.OK() {
		t.Fatalf("no env_schema should pass, got %+v", sc)
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
