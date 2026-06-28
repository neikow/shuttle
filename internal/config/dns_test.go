package config

import (
	"os"
	"path/filepath"
	"testing"
)

const validDNSYML = `providers:
  - name: ovh
    type: ovh
    endpoint: ovh-eu
    credentials:
      application_key:    { infisical_key: OVH_APP_KEY }
      application_secret: { infisical_key: OVH_APP_SECRET }
      consumer_key:       { infisical_key: OVH_CONSUMER_KEY }
certificates:
  - name: star-example
    domains: ["*.example.com", "example.com"]
    provider: ovh
`

func writeRepoWithDNS(t *testing.T, dnsYML, svcYAML string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "hosts.yaml"), "hosts:\n  - name: web1\n")
	if dnsYML != "" {
		writeFile(t, filepath.Join(dir, "dns.yml"), dnsYML)
	}
	svcDir := filepath.Join(dir, "services", "app")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(svcDir, "app.yaml"), svcYAML)
	writeFile(t, filepath.Join(svcDir, "docker-compose.yml"), "services: {}\n")
	return dir
}

func TestLoad_dns_valid(t *testing.T) {
	dir := writeRepoWithDNS(t, validDNSYML,
		"name: app\nhost: web1\ndomains: [app.example.com]\ntls_certificate: star-example\n")
	repo, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if repo.DNS == nil || len(repo.DNS.Certificates) != 1 {
		t.Fatalf("DNS not parsed: %+v", repo.DNS)
	}
	if got := repo.DNS.Providers[0].Credentials["consumer_key"].InfisicalKey; got != "OVH_CONSUMER_KEY" {
		t.Errorf("consumer_key ref = %q, want OVH_CONSUMER_KEY", got)
	}
	if repo.Services[0].TLSCertificate != "star-example" {
		t.Errorf("pin = %q, want star-example", repo.Services[0].TLSCertificate)
	}
}

func TestLoad_dns_cloudflareRoute53(t *testing.T) {
	dns := `providers:
  - name: cf
    type: cloudflare
    credentials:
      api_token: { infisical_key: CF_TOKEN }
  - name: aws
    type: route53
    credentials:
      access_key_id:     { infisical_key: AWS_KEY }
      secret_access_key: { infisical_key: AWS_SECRET }
      region:            { infisical_key: AWS_REGION }
certificates:
  - name: star
    domains: ["*.example.com"]
    provider: cf
`
	dir := writeRepoWithDNS(t, dns, "name: app\nhost: web1\ndomains: [app.example.com]\n")
	repo, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(repo.DNS.Providers) != 2 {
		t.Fatalf("want 2 providers, got %d", len(repo.DNS.Providers))
	}
	// cloudflare/route53 need no endpoint.
	if repo.DNS.Providers[0].Endpoint != "" {
		t.Errorf("cloudflare endpoint = %q, want empty", repo.DNS.Providers[0].Endpoint)
	}
	if got := repo.DNS.Providers[1].Credentials["region"].InfisicalKey; got != "AWS_REGION" {
		t.Errorf("route53 region ref = %q, want AWS_REGION", got)
	}
}

func TestLoad_dns_absent(t *testing.T) {
	dir := writeRepoWithDNS(t, "", "name: app\nhost: web1\ndomains: [app.example.com]\n")
	repo, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if repo.DNS != nil {
		t.Errorf("expected nil DNS without dns.yml, got %+v", repo.DNS)
	}
}

func TestLoad_dns_invalid(t *testing.T) {
	svc := "name: app\nhost: web1\ndomains: [app.example.com]\n"
	tests := map[string]struct {
		dns, svc string
	}{
		"unknown provider type": {
			dns: "providers:\n  - name: p\n    type: bogus\n    endpoint: x\n    credentials: {}\ncertificates: []\n",
			svc: svc,
		},
		"missing required cred": {
			dns: "providers:\n  - name: ovh\n    type: ovh\n    endpoint: ovh-eu\n    credentials:\n      application_key: { infisical_key: K }\ncertificates: []\n",
			svc: svc,
		},
		"endpoint required": {
			dns: "providers:\n  - name: ovh\n    type: ovh\n    credentials:\n      application_key: { infisical_key: K }\n      application_secret: { infisical_key: S }\n      consumer_key: { infisical_key: C }\ncertificates: []\n",
			svc: svc,
		},
		"cert unknown provider": {
			dns: validDNSYML + "  - name: other\n    domains: [\"*.x.com\"]\n    provider: nope\n",
			svc: svc,
		},
		"duplicate provider name": {
			dns: "providers:\n" +
				"  - name: ovh\n    type: ovh\n    endpoint: ovh-eu\n    credentials:\n      application_key: { infisical_key: K }\n      application_secret: { infisical_key: S }\n      consumer_key: { infisical_key: C }\n" +
				"  - name: ovh\n    type: ovh\n    endpoint: ovh-ca\n    credentials:\n      application_key: { infisical_key: K }\n      application_secret: { infisical_key: S }\n      consumer_key: { infisical_key: C }\n" +
				"certificates: []\n",
			svc: svc,
		},
		"pin to missing cert": {
			dns: validDNSYML,
			svc: "name: app\nhost: web1\ndomains: [app.example.com]\ntls_certificate: nonexistent\n",
		},
		"zone unknown provider": {
			dns: validDNSYML + "zones:\n  - domain: example.com\n    provider: nope\n",
			svc: svc,
		},
		"zone provider cannot manage records": {
			// cloudflare is ACME-only (no records capability) → invalid as a zone provider.
			dns: "providers:\n  - name: cf\n    type: cloudflare\n    credentials:\n      api_token: { infisical_key: T }\n" +
				"zones:\n  - domain: example.com\n    provider: cf\n",
			svc: svc,
		},
		"cert provider cannot issue (manual)": {
			dns: "providers:\n  - name: m\n    type: manual\n" +
				"certificates:\n  - name: c\n    domains: [\"*.example.com\"]\n    provider: m\n",
			svc: svc,
		},
		"duplicate zone domain": {
			dns: validDNSYML + "zones:\n  - domain: example.com\n    provider: ovh\n  - domain: example.com\n    provider: ovh\n",
			svc: svc,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := writeRepoWithDNS(t, tc.dns, tc.svc)
			if _, err := Load(dir); err == nil {
				t.Fatal("expected validation error, got nil")
			}
		})
	}
}

func TestDomainCoveredBy(t *testing.T) {
	tests := []struct {
		domain, subject string
		want            bool
	}{
		{"app.example.com", "app.example.com", true}, // exact
		{"app.example.com", "*.example.com", true},   // wildcard one label
		{"example.com", "*.example.com", false},      // apex not covered by wildcard
		{"a.b.example.com", "*.example.com", false},  // deeper not covered
		{"app.example.com", "*.other.com", false},    // different zone
		{"example.com", "example.com", true},         // apex exact
	}
	for _, tc := range tests {
		if got := DomainCoveredBy(tc.domain, tc.subject); got != tc.want {
			t.Errorf("DomainCoveredBy(%q,%q) = %v, want %v", tc.domain, tc.subject, got, tc.want)
		}
	}
}

const zonesDNSYML = `providers:
  - name: ovh
    type: ovh
    endpoint: ovh-eu
    credentials:
      application_key:    { infisical_key: OVH_APP_KEY }
      application_secret: { infisical_key: OVH_APP_SECRET }
      consumer_key:       { infisical_key: OVH_CONSUMER_KEY }
  - name: home
    type: manual
zones:
  - domain: example.com
    provider: ovh
  - domain: home.example.com
    provider: home
    address: tailscale
`

func TestLoad_dns_zones(t *testing.T) {
	dir := writeRepoWithDNS(t, zonesDNSYML, "name: app\nhost: web1\ndomains: [app.example.com]\n")
	repo, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(repo.DNS.Zones); got != 2 {
		t.Fatalf("zones = %d, want 2", got)
	}
	if p := repo.DNS.ProviderByName("home"); p == nil || p.Type != "manual" {
		t.Errorf("ProviderByName(home) = %+v, want manual provider", p)
	}
}

func TestZoneFor_LongestSuffix(t *testing.T) {
	d := &DNSConfig{Zones: []DNSZone{
		{Domain: "example.com", Provider: "ovh"},
		{Domain: "home.example.com", Provider: "home", Address: "tailscale"},
	}}
	cases := map[string]string{
		"portfolio.example.com": "example.com",      // only the public zone matches
		"app.home.example.com":  "home.example.com", // longest suffix wins
		"home.example.com":      "home.example.com", // exact
		"example.com":           "example.com",      // apex
		"notexample.com":        "",                 // not a dot-bounded suffix
		"other.net":             "",                 // no match
	}
	for domain, want := range cases {
		z := d.ZoneFor(domain)
		got := ""
		if z != nil {
			got = z.Domain
		}
		if got != want {
			t.Errorf("ZoneFor(%q) = %q, want %q", domain, got, want)
		}
	}
}

func TestHostAddress(t *testing.T) {
	h := Host{Addresses: map[string]string{"public": "203.0.113.20", "tailscale": "100.64.0.5"}}
	if got := h.Address(""); got != "203.0.113.20" {
		t.Errorf("Address(\"\") = %q, want public default", got)
	}
	if got := h.Address("tailscale"); got != "100.64.0.5" {
		t.Errorf("Address(tailscale) = %q", got)
	}
	if got := h.Address("missing"); got != "" {
		t.Errorf("Address(missing) = %q, want empty", got)
	}
}
