package orchestrator

import (
	"context"
	"reflect"
	"testing"

	"github.com/neikow/shuttle/internal/secrets"
)

func TestFingerprintSecrets(t *testing.T) {
	a := fingerprintSecrets(map[string]string{"A": "1", "B": "2"})
	// Key order must not matter.
	b := fingerprintSecrets(map[string]string{"B": "2", "A": "1"})
	if a != b {
		t.Errorf("fingerprint not order-independent: %q vs %q", a, b)
	}
	// A value change must change the fingerprint.
	if c := fingerprintSecrets(map[string]string{"A": "1", "B": "3"}); c == a {
		t.Error("fingerprint unchanged after value change")
	}
	// "A=1,B=2" must not collide with "A=1B,=2" style concatenation ambiguity.
	if x, y := fingerprintSecrets(map[string]string{"A": "1", "B": "2"}),
		fingerprintSecrets(map[string]string{"A": "1B", "": "2"}); x == y {
		t.Error("fingerprint collision across key/value boundary")
	}
}

func TestSecretPoller_diffScopes(t *testing.T) {
	fake := secrets.NewFake(nil)
	base := secrets.Scope{Env: "prod", Path: "/shared"}
	svc := secrets.Scope{Env: "prod", Path: "/services/api"}
	fake.SetScope(base, map[string]string{"SHARED": "v1"})
	fake.SetScope(svc, map[string]string{"API_KEY": "k1"})

	g := &GitSyncer{secrets: fake, secretsBasePath: "/shared", secretsPathTemplate: "/services/{service}"}
	p := NewSecretPoller(g, 0, "prod")
	scopes := []SecretChange{{Env: "prod", Path: "/shared"}, {Env: "prod", Path: "/services/api"}}

	// First pass seeds; reports no changes.
	if got := p.diffScopes(context.Background(), scopes); got != nil {
		t.Fatalf("first pass reported changes: %v", got)
	}
	// No change => still nothing.
	if got := p.diffScopes(context.Background(), scopes); got != nil {
		t.Fatalf("unchanged poll reported changes: %v", got)
	}
	// Change one scope's value => only that scope reported.
	fake.SetScope(svc, map[string]string{"API_KEY": "k2"})
	got := p.diffScopes(context.Background(), scopes)
	want := []SecretChange{{Env: "prod", Path: "/services/api"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changed poll = %v, want %v", got, want)
	}
}
