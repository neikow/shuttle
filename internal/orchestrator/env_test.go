package orchestrator

import (
	"context"
	"testing"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/secrets"
)

func TestRenderEnv_scopeAndSchema(t *testing.T) {
	sec := secrets.NewFake(map[string]string{"GLOBAL": "g"})
	sec.SetScope("staging", map[string]string{"API_KEY": "staging-key", "EXTRA": "x"})
	syncer := NewGitSyncer("", "main", t.TempDir(), nil, NewRegistry(), sec)
	ctx := context.Background()

	// env_from selects the scope; env_schema filters to declared keys.
	env, err := syncer.renderEnv(ctx, config.Service{
		Name: "api", Host: "web1", EnvFrom: "staging", EnvSchema: []string{"API_KEY"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 1 || env["API_KEY"] != "staging-key" {
		t.Fatalf("scoped+filtered env = %v, want {API_KEY: staging-key}", env)
	}

	// No schema: all secrets in the scope pass through.
	env, err = syncer.renderEnv(ctx, config.Service{Name: "api2", EnvFrom: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 2 || env["EXTRA"] != "x" {
		t.Fatalf("scoped passthrough env = %v, want {API_KEY, EXTRA}", env)
	}

	// Empty env_from uses the provider's default set, not a named scope.
	env, err = syncer.renderEnv(ctx, config.Service{Name: "api3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 1 || env["GLOBAL"] != "g" {
		t.Fatalf("default-scope env = %v, want {GLOBAL: g}", env)
	}
}

func TestRenderEnv_noProvider(t *testing.T) {
	syncer := NewGitSyncer("", "main", t.TempDir(), nil, NewRegistry(), nil)
	env, err := syncer.renderEnv(context.Background(), config.Service{Name: "x", EnvFrom: "staging"})
	if err != nil || env != nil {
		t.Fatalf("no provider should yield (nil, nil), got %v %v", env, err)
	}
}
