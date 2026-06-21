package orchestrator

import (
	"context"
	"testing"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/secrets"
)

func ovhRepo(services []config.Service, certs []config.DNSCertificate) *config.Repo {
	return &config.Repo{
		Services: services,
		DNS: &config.DNSConfig{
			Providers: []config.DNSProvider{{
				Name:     "ovh",
				Type:     "ovh",
				Endpoint: "ovh-eu",
				Credentials: map[string]config.SecretRef{
					"application_key":    {InfisicalKey: "OVH_APP_KEY"},
					"application_secret": {InfisicalKey: "OVH_APP_SECRET"},
					"consumer_key":       {InfisicalKey: "OVH_CONSUMER_KEY"},
				},
			}},
			Certificates: certs,
		},
	}
}

func fakeOVHSecrets() *secrets.Fake {
	return secrets.NewFake(map[string]string{
		"OVH_APP_KEY":      "ak",
		"OVH_APP_SECRET":   "as",
		"OVH_CONSUMER_KEY": "ck",
	})
}

// policyProvider digs the resolved DNS provider object out of a policy.
func policyProvider(t *testing.T, p map[string]any) map[string]any {
	t.Helper()
	issuers := p["issuers"].([]any)
	ch := issuers[0].(map[string]any)["challenges"].(map[string]any)
	return ch["dns"].(map[string]any)["provider"].(map[string]any)
}

func TestResolveTLSPolicies_autoMatch(t *testing.T) {
	repo := ovhRepo(
		[]config.Service{{Name: "app", Host: "web1", Domains: []string{"app.example.com"}, Port: 80}},
		[]config.DNSCertificate{{Name: "star", Domains: []string{"*.example.com", "example.com"}, Provider: "ovh"}},
	)
	g := &GitSyncer{secrets: fakeOVHSecrets()}

	policies, err := g.resolveTLSPolicies(context.Background(), repo, "web1")
	if err != nil {
		t.Fatalf("resolveTLSPolicies: %v", err)
	}
	if len(policies) != 1 {
		t.Fatalf("want 1 policy, got %d", len(policies))
	}
	subjects := policies[0]["subjects"].([]string)
	if len(subjects) != 2 || subjects[0] != "*.example.com" || subjects[1] != "example.com" {
		t.Errorf("subjects = %v, want sorted [*.example.com example.com]", subjects)
	}
	prov := policyProvider(t, policies[0])
	if prov["name"] != "ovh" || prov["endpoint"] != "ovh-eu" {
		t.Errorf("provider = %v, want ovh/ovh-eu", prov)
	}
	if prov["application_key"] != "ak" || prov["consumer_key"] != "ck" {
		t.Errorf("creds not injected: %v", prov)
	}
}

func TestResolveTLSPolicies_fallbackWhenUncovered(t *testing.T) {
	repo := ovhRepo(
		[]config.Service{{Name: "app", Host: "web1", Domains: []string{"app.other.com"}, Port: 80}},
		[]config.DNSCertificate{{Name: "star", Domains: []string{"*.example.com"}, Provider: "ovh"}},
	)
	g := &GitSyncer{secrets: fakeOVHSecrets()}
	policies, err := g.resolveTLSPolicies(context.Background(), repo, "web1")
	if err != nil {
		t.Fatalf("resolveTLSPolicies: %v", err)
	}
	if policies != nil {
		t.Errorf("uncovered domain should yield no policy (HTTP-01 fallback), got %v", policies)
	}
}

func TestResolveTLSPolicies_pinForcesUncovered(t *testing.T) {
	repo := ovhRepo(
		[]config.Service{{Name: "app", Host: "web1", Domains: []string{"app.other.com"}, Port: 80, TLSCertificate: "star"}},
		[]config.DNSCertificate{{Name: "star", Domains: []string{"*.example.com"}, Provider: "ovh"}},
	)
	g := &GitSyncer{secrets: fakeOVHSecrets()}
	policies, err := g.resolveTLSPolicies(context.Background(), repo, "web1")
	if err != nil {
		t.Fatalf("resolveTLSPolicies: %v", err)
	}
	if len(policies) != 1 {
		t.Fatalf("pin should force a policy, got %d", len(policies))
	}
	subjects := policies[0]["subjects"].([]string)
	// Pin adds the service's domain to the cert's declared subjects.
	want := map[string]bool{"*.example.com": true, "app.other.com": true}
	if len(subjects) != 2 {
		t.Fatalf("subjects = %v, want the cert subject + the pinned domain", subjects)
	}
	for _, s := range subjects {
		if !want[s] {
			t.Errorf("unexpected subject %q in %v", s, subjects)
		}
	}
}

func TestResolveTLSPolicies_perHostFiltering(t *testing.T) {
	repo := ovhRepo(
		[]config.Service{
			{Name: "app", Host: "web1", Domains: []string{"app.example.com"}, Port: 80},
			{Name: "api", Host: "web2", Domains: []string{"api.other.com"}, Port: 80},
		},
		[]config.DNSCertificate{{Name: "star", Domains: []string{"*.example.com"}, Provider: "ovh"}},
	)
	g := &GitSyncer{secrets: fakeOVHSecrets()}

	if p, _ := g.resolveTLSPolicies(context.Background(), repo, "web1"); len(p) != 1 {
		t.Errorf("web1 serves a covered domain; want 1 policy, got %d", len(p))
	}
	if p, _ := g.resolveTLSPolicies(context.Background(), repo, "web2"); p != nil {
		t.Errorf("web2 serves nothing under the cert; want no policy, got %v", p)
	}
}

func TestResolveTLSPolicies_missingSecret(t *testing.T) {
	repo := ovhRepo(
		[]config.Service{{Name: "app", Host: "web1", Domains: []string{"app.example.com"}, Port: 80}},
		[]config.DNSCertificate{{Name: "star", Domains: []string{"*.example.com"}, Provider: "ovh"}},
	)
	g := &GitSyncer{secrets: secrets.NewFake(nil)} // no creds seeded
	if _, err := g.resolveTLSPolicies(context.Background(), repo, "web1"); err == nil {
		t.Fatal("expected error when a provider credential is missing")
	}
}

func TestResolveTLSPolicies_noDNS(t *testing.T) {
	repo := &config.Repo{Services: []config.Service{{Name: "app", Host: "web1", Domains: []string{"x.com"}}}}
	g := &GitSyncer{secrets: fakeOVHSecrets()}
	if p, err := g.resolveTLSPolicies(context.Background(), repo, "web1"); err != nil || p != nil {
		t.Errorf("no dns.yml => nil policies, got p=%v err=%v", p, err)
	}
}

func TestBuildCaddyConfig_tlsPolicies(t *testing.T) {
	routes := []CaddyRoute{{Domain: "app.example.com", Upstream: "app:80"}}
	policy := caddyTLSPolicy([]string{"*.example.com"}, map[string]any{"name": "ovh"})

	cfg := buildCaddyConfig(routes, false, 0, 0, []map[string]any{policy})
	apps := cfg["apps"].(map[string]any)
	tls, ok := apps["tls"].(map[string]any)
	if !ok {
		t.Fatalf("expected apps.tls block, got %v", apps)
	}
	policies := tls["automation"].(map[string]any)["policies"].([]map[string]any)
	if len(policies) != 1 {
		t.Fatalf("want 1 policy, got %d", len(policies))
	}

	// No policies -> no tls block (HTTP-01 default).
	if _, ok := buildCaddyConfig(routes, false, 0, 0, nil)["apps"].(map[string]any)["tls"]; ok {
		t.Error("nil policies should not add a tls block")
	}
}
