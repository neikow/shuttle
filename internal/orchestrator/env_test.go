package orchestrator

import (
	"context"
	"testing"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/secrets"
)

func newSecretsSyncer(t *testing.T, sec secrets.Provider, base, template string) *GitSyncer {
	t.Helper()
	s := NewGitSyncer("", "main", t.TempDir(), nil, NewRegistry(), sec)
	s.SetSecretsPaths(base, template)
	return s
}

func TestRenderEnv_baseAndServiceMerge(t *testing.T) {
	sec := secrets.NewFake(nil)
	sec.SetScope(secrets.Scope{Env: "prod", Path: "/shared"}, map[string]string{"DB_URL": "shared-db", "COMMON": "c"})
	sec.SetScope(secrets.Scope{Env: "prod", Path: "/services/api"}, map[string]string{"API_KEY": "k", "DB_URL": "svc-db"})

	syncer := newSecretsSyncer(t, sec, "/shared", "/services/{service}")
	ctx := context.Background()

	// No schema: shared + service merged, service overrides DB_URL.
	env, err := syncer.renderEnv(ctx, config.Service{Name: "api", EnvFrom: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	if env["DB_URL"] != "svc-db" || env["COMMON"] != "c" || env["API_KEY"] != "k" || len(env) != 3 {
		t.Fatalf("merged env = %v, want {DB_URL:svc-db, COMMON:c, API_KEY:k}", env)
	}

	// With schema: filtered to declared keys.
	env, err = syncer.renderEnv(ctx, config.Service{Name: "api", EnvFrom: "prod", EnvSchema: []string{"API_KEY", "DB_URL"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 2 || env["API_KEY"] != "k" || env["DB_URL"] != "svc-db" {
		t.Fatalf("filtered env = %v, want {API_KEY, DB_URL}", env)
	}
}

func TestRenderEnv_explicitSecretPathOverridesTemplate(t *testing.T) {
	sec := secrets.NewFake(nil)
	sec.SetScope(secrets.Scope{Env: "prod", Path: "/shared"}, map[string]string{"COMMON": "c"})
	sec.SetScope(secrets.Scope{Env: "prod", Path: "/custom"}, map[string]string{"X": "from-custom"})

	syncer := newSecretsSyncer(t, sec, "/shared", "/services/{service}")
	env, err := syncer.renderEnv(context.Background(), config.Service{Name: "api", EnvFrom: "prod", SecretPath: "/custom"})
	if err != nil {
		t.Fatal(err)
	}
	if env["X"] != "from-custom" || env["COMMON"] != "c" {
		t.Fatalf("env = %v, want service folder /custom merged over base", env)
	}
}

func TestRenderEnv_noTemplateUsesBaseOnly(t *testing.T) {
	sec := secrets.NewFake(nil)
	sec.SetScope(secrets.Scope{Env: "prod", Path: "/shared"}, map[string]string{"ONLY": "base"})

	syncer := newSecretsSyncer(t, sec, "/shared", "") // no template, no per-service path
	env, err := syncer.renderEnv(context.Background(), config.Service{Name: "api", EnvFrom: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 1 || env["ONLY"] != "base" {
		t.Fatalf("env = %v, want only base secrets", env)
	}
}

func TestRenderEnv_noProvider(t *testing.T) {
	syncer := newSecretsSyncer(t, nil, "/shared", "")
	env, err := syncer.renderEnv(context.Background(), config.Service{Name: "x", EnvFrom: "prod"})
	if err != nil || env != nil {
		t.Fatalf("no provider should yield (nil, nil), got %v %v", env, err)
	}
}
