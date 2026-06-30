package secrets

import (
	"errors"
	"testing"
)

func TestInfisicalEnvPathFor(t *testing.T) {
	p := &InfisicalProvider{environment: "production", secretPath: "/base"}
	if got := p.envFor(Scope{}); got != "production" {
		t.Errorf("envFor default = %q, want production", got)
	}
	if got := p.envFor(Scope{Env: "staging"}); got != "staging" {
		t.Errorf("envFor override = %q, want staging", got)
	}
	if got := p.pathFor(Scope{}); got != "/base" {
		t.Errorf("pathFor default = %q, want /base", got)
	}
	if got := p.pathFor(Scope{Path: "/svc"}); got != "/svc" {
		t.Errorf("pathFor override = %q, want /svc", got)
	}
}

func TestNewProvider(t *testing.T) {
	for _, name := range []string{"", "none"} {
		p, err := NewProvider(name)
		if err != nil || p != nil {
			t.Errorf("NewProvider(%q) = %v,%v want nil,nil", name, p, err)
		}
	}
	if _, err := NewProvider("bogus"); err == nil {
		t.Error("unknown provider should error")
	}

	t.Setenv("SHUTTLE_SECRETS_DIR", t.TempDir())
	if p, err := NewProvider("file"); err != nil || p == nil {
		t.Errorf("NewProvider(file) = %v,%v want a provider", p, err)
	}
	t.Setenv("SHUTTLE_SECRETS_DIR", "")
	if _, err := NewProvider("file"); err == nil {
		t.Error("file provider without SHUTTLE_SECRETS_DIR should error")
	}
}

func TestNewInfisical_MissingCreds(t *testing.T) {
	t.Setenv("INFISICAL_CLIENT_ID", "")
	t.Setenv("INFISICAL_CLIENT_SECRET", "")
	t.Setenv("INFISICAL_PROJECT_ID", "")
	if _, err := NewInfisical(); err == nil {
		t.Error("NewInfisical without credentials should error")
	}
}

func TestErrNotFound(t *testing.T) {
	err := ErrNotFound{Key: "API_KEY"}
	if err.Error() != "secret not found: API_KEY" {
		t.Errorf("Error() = %q", err.Error())
	}
	var e ErrNotFound
	if !errors.As(error(ErrNotFound{Key: "x"}), &e) {
		t.Error("ErrNotFound should match errors.As")
	}
}
