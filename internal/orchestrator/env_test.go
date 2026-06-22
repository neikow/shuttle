package orchestrator

import (
	"context"
	"strings"
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

	// Empty values pull from the merged provider folders; the service folder
	// overrides the shared base for DB_URL.
	env, err := syncer.renderEnv(ctx, config.Service{Name: "api", EnvFrom: "prod", Env: map[string]string{
		"DB_URL": "", "COMMON": "", "API_KEY": "",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if env["DB_URL"] != "svc-db" || env["COMMON"] != "c" || env["API_KEY"] != "k" || len(env) != 3 {
		t.Fatalf("merged env = %v, want {DB_URL:svc-db, COMMON:c, API_KEY:k}", env)
	}
}

func TestRenderEnv_renameViaSecretToken(t *testing.T) {
	sec := secrets.NewFake(nil)
	sec.SetScope(secrets.Scope{Env: "prod", Path: "/services/api"}, map[string]string{"DATABASE_URL": "v"})

	syncer := newSecretsSyncer(t, sec, "/shared", "/services/{service}")
	// ${infisical:KEY} / ${secret:KEY} fetch a provider key under a different name.
	env, err := syncer.renderEnv(context.Background(), config.Service{Name: "api", EnvFrom: "prod", Env: map[string]string{
		"DB_URL": "${infisical:DATABASE_URL}",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if env["DB_URL"] != "v" || len(env) != 1 {
		t.Fatalf("env = %v, want {DB_URL:v}", env)
	}
}

func TestRenderEnv_processEnvAndLiteralNeedNoProvider(t *testing.T) {
	t.Setenv("DEPLOY_REGION", "eu-west")
	// No provider configured at all: literals and ${env:KEY} still resolve.
	syncer := newSecretsSyncer(t, nil, "/shared", "")
	env, err := syncer.renderEnv(context.Background(), config.Service{Name: "api", Env: map[string]string{
		"REGION":   "${env:DEPLOY_REGION}",
		"LOG":      "info",
		"ENDPOINT": "https://${env:DEPLOY_REGION}.example.com",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if env["REGION"] != "eu-west" || env["LOG"] != "info" || env["ENDPOINT"] != "https://eu-west.example.com" {
		t.Fatalf("env = %v", env)
	}
}

func TestRenderEnv_providerRefWithoutProviderErrors(t *testing.T) {
	syncer := newSecretsSyncer(t, nil, "/shared", "")
	_, err := syncer.renderEnv(context.Background(), config.Service{Name: "api", Env: map[string]string{
		"DB": "",
	}})
	if err == nil || !strings.Contains(err.Error(), "no secrets_provider") {
		t.Fatalf("want no-provider error, got %v", err)
	}
}

func TestRenderEnv_explicitSecretPathOverridesTemplate(t *testing.T) {
	sec := secrets.NewFake(nil)
	sec.SetScope(secrets.Scope{Env: "prod", Path: "/shared"}, map[string]string{"COMMON": "c"})
	sec.SetScope(secrets.Scope{Env: "prod", Path: "/custom"}, map[string]string{"X": "from-custom"})

	syncer := newSecretsSyncer(t, sec, "/shared", "/services/{service}")
	env, err := syncer.renderEnv(context.Background(), config.Service{Name: "api", EnvFrom: "prod", SecretPath: "/custom", Env: map[string]string{
		"X": "", "COMMON": "",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if env["X"] != "from-custom" || env["COMMON"] != "c" {
		t.Fatalf("env = %v, want service folder /custom merged over base", env)
	}
}

func TestRenderEnv_unsetTemplateDefaultsToServicesFolder(t *testing.T) {
	sec := secrets.NewFake(nil)
	sec.SetScope(secrets.Scope{Env: "prod", Path: "/shared"}, map[string]string{"ONLY": "base"})
	sec.SetScope(secrets.Scope{Env: "prod", Path: "/services/api"}, map[string]string{"SVC": "v"})

	syncer := newSecretsSyncer(t, sec, "/shared", "") // unset template -> /services/{service}
	env, err := syncer.renderEnv(context.Background(), config.Service{Name: "api", EnvFrom: "prod", Env: map[string]string{
		"ONLY": "", "SVC": "",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 2 || env["ONLY"] != "base" || env["SVC"] != "v" {
		t.Fatalf("env = %v, want base + default /services/api secrets", env)
	}
}

func TestRenderEnv_missingRefErrors(t *testing.T) {
	sec := secrets.NewFake(nil)
	sec.SetScope(secrets.Scope{Env: "prod", Path: "/shared"}, map[string]string{"API_KEY": "k"})

	syncer := newSecretsSyncer(t, sec, "/shared", "")
	env, err := syncer.renderEnv(context.Background(), config.Service{
		Name: "api", EnvFrom: "prod", Env: map[string]string{
			"API_KEY": "", "DB_URL": "", "MISSING": "",
		},
	})
	if err == nil {
		t.Fatalf("want error for missing refs, got env %v", env)
	}
	for _, want := range []string{"DB_URL", "MISSING"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should name missing key %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "API_KEY") {
		t.Errorf("error %q should not name present key API_KEY", err)
	}
}

// A service with no env reads nothing and never touches the provider — so a
// missing/absent Infisical folder doesn't fail the deploy.
func TestRenderEnv_noEnvSkipsProvider(t *testing.T) {
	sec := secrets.NewFake(nil) // no scopes set -> any GetAll would be empty/missing
	syncer := newSecretsSyncer(t, sec, "/shared", "")
	env, err := syncer.renderEnv(context.Background(), config.Service{Name: "x", EnvFrom: "prod"})
	if err != nil || env != nil {
		t.Fatalf("no env should yield (nil, nil) with no provider call, got %v %v", env, err)
	}
}

func TestRenderEnv_noProvider(t *testing.T) {
	syncer := newSecretsSyncer(t, nil, "/shared", "")
	env, err := syncer.renderEnv(context.Background(), config.Service{Name: "x", EnvFrom: "prod"})
	if err != nil || env != nil {
		t.Fatalf("no provider should yield (nil, nil), got %v %v", env, err)
	}
}
