package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DNSConfig is the repo's optional dns.yml: DNS-challenge certificate providers
// and the certificates (incl. wildcards) they issue. It exists so a service's
// domains can be served by a wildcard cert provisioned via a DNS-01 challenge —
// the only ACME challenge that can issue wildcards and that works without the
// host being reachable on :80/:443. A domain not covered by any certificate
// falls back to Caddy's default automation (per-domain HTTP-01 Let's Encrypt).
type DNSConfig struct {
	Providers    []DNSProvider    `yaml:"providers"`
	Certificates []DNSCertificate `yaml:"certificates"`
	// Zones map a domain suffix to the provider that manages its A/AAAA records
	// (record management, distinct from the ACME challenge that Certificates use).
	Zones []DNSZone `yaml:"zones"`
}

// DNSProvider is a named DNS provider. Type selects the capability and (for ACME)
// the Caddy DNS module and thus the credential keys; Credentials map each
// provider field to a secret reference resolved from the secrets provider at
// config-build time (injected inline into the pushed Caddy config, or used by the
// record-management client — never written to disk). Some types are ACME-only
// (cloudflare/route53), some manage records (ovh/manual), per dnsProviderSpecs.
type DNSProvider struct {
	Name        string               `yaml:"name"`
	Type        string               `yaml:"type"`     // e.g. "ovh", "manual", "sidecar"
	Endpoint    string               `yaml:"endpoint"` // provider-specific (OVH: ovh-eu, ovh-ca, ...)
	Credentials map[string]SecretRef `yaml:"credentials"`
	// Host names the host whose agent runs the CoreDNS sidecar (sidecar type
	// only). Port is the host port that sidecar publishes for :53 (default 53).
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// SidecarPort returns the sidecar's host port, defaulting to 53.
func (p DNSProvider) SidecarPort() int {
	if p.Port == 0 {
		return DefaultDNSSidecarPort
	}
	return p.Port
}

// DefaultDNSSidecarPort is the host port the CoreDNS sidecar publishes when a
// sidecar provider names none.
const DefaultDNSSidecarPort = 53

// DNSZone binds a domain suffix to a record-management provider. A service's
// domain is matched to the longest zone whose Domain is a suffix of it; the
// matched zone's Provider then creates/updates the A/AAAA record pointing at the
// service's host Address(Label). This is how a single project drives split DNS —
// e.g. a public zone via OVH and a private (Tailscale) zone via the sidecar.
type DNSZone struct {
	Domain   string `yaml:"domain"`   // suffix, e.g. "home.example.com" (no scheme)
	Provider string `yaml:"provider"` // a DNSProvider name whose type can manage records
	Address  string `yaml:"address"`  // host-address label to point records at (default "public")
}

// DNSCertificate is one certificate (a Caddy TLS automation policy): the
// Domains are its subjects (a wildcard like "*.example.com" plus optionally the
// apex), issued via Provider's DNS challenge.
type DNSCertificate struct {
	Name     string   `yaml:"name"`
	Domains  []string `yaml:"domains"`
	Provider string   `yaml:"provider"`
}

// SecretRef points at a value in the secrets provider. Mirrors the lookup-scope
// fields of GitCredential / BackupCredential.
type SecretRef struct {
	InfisicalKey  string `yaml:"infisical_key"`
	InfisicalEnv  string `yaml:"infisical_env"`
	InfisicalPath string `yaml:"infisical_path"`
}

// dnsProviderSpec describes what a supported provider type requires and what it
// can do. The required credential keys are also the Caddy DNS provider JSON field
// names, so the resolved credentials map straight onto the provider config. The
// acme/records flags gate which sections may reference the provider: certificates
// need an ACME-capable type, zones need a record-capable type.
type dnsProviderSpec struct {
	requiredCreds    []string
	endpointRequired bool
	acme             bool // usable by certificates[] (a Caddy DNS-01 module exists)
	records          bool // usable by zones[] (A/AAAA record management)
}

// dnsProviderSpecs is the registry of supported DNS provider types. An ACME type
// is supported only when the shipped Caddy image (shuttle-caddy) bundles its
// plugin — add the plugin and the spec together. The credential keys are the
// Caddy DNS provider's own JSON field names, so the resolved values map straight
// onto its config object (see caddyDNSProvider). Record-management support is
// independent: "manual" is a no-op provider (the user creates records); "ovh"
// does both (the "sidecar" private-DNS type is added in a later change).
var dnsProviderSpecs = map[string]dnsProviderSpec{
	"ovh":        {requiredCreds: []string{"application_key", "application_secret", "consumer_key"}, endpointRequired: true, acme: true, records: true},
	"cloudflare": {requiredCreds: []string{"api_token"}, acme: true},
	"route53":    {requiredCreds: []string{"access_key_id", "secret_access_key", "region"}, acme: true},
	"manual":     {records: true},
	"sidecar":    {records: true},
}

// loadDNS reads the optional dns.yml at the repo root. A missing file yields
// (nil, nil) — DNS challenges are opt-in.
func loadDNS(rootDir string) (*DNSConfig, error) {
	path := filepath.Join(rootDir, "dns.yml")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg DNSConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		// An empty/comment-only file declares no DNS config.
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return &cfg, nil
}

// validate checks the DNS config in isolation (provider/cert integrity). Service
// pins are validated against it in Repo.Validate, which has the services.
func (d *DNSConfig) validate() error {
	provTypes := make(map[string]string, len(d.Providers)) // name -> type
	for i, p := range d.Providers {
		if p.Name == "" {
			return fmt.Errorf("dns provider[%d]: name is required", i)
		}
		if _, dup := provTypes[p.Name]; dup {
			return fmt.Errorf("dns provider %q: duplicate name", p.Name)
		}
		provTypes[p.Name] = p.Type

		spec, ok := dnsProviderSpecs[p.Type]
		if !ok {
			return fmt.Errorf("dns provider %q: unsupported type %q (supported: %s)", p.Name, p.Type, supportedDNSTypes())
		}
		if spec.endpointRequired && p.Endpoint == "" {
			return fmt.Errorf("dns provider %q: endpoint is required for type %q", p.Name, p.Type)
		}
		for _, key := range spec.requiredCreds {
			ref, ok := p.Credentials[key]
			if !ok {
				return fmt.Errorf("dns provider %q: missing credential %q (required for type %q)", p.Name, key, p.Type)
			}
			if ref.InfisicalKey == "" {
				return fmt.Errorf("dns provider %q: credential %q: infisical_key is required", p.Name, key)
			}
		}
		if p.Type == "sidecar" {
			if p.Host == "" {
				return fmt.Errorf("dns provider %q: host is required for type \"sidecar\" (the host whose agent runs CoreDNS)", p.Name)
			}
			if p.Port != 0 && (p.Port < 1 || p.Port > 65535) {
				return fmt.Errorf("dns provider %q: port %d out of range 1-65535", p.Name, p.Port)
			}
		}
	}

	certs := make(map[string]bool, len(d.Certificates))
	for i, c := range d.Certificates {
		if c.Name == "" {
			return fmt.Errorf("dns certificate[%d]: name is required", i)
		}
		if certs[c.Name] {
			return fmt.Errorf("dns certificate %q: duplicate name", c.Name)
		}
		certs[c.Name] = true
		if len(c.Domains) == 0 {
			return fmt.Errorf("dns certificate %q: at least one domain is required", c.Name)
		}
		typ, known := provTypes[c.Provider]
		if !known {
			return fmt.Errorf("dns certificate %q: references unknown provider %q", c.Name, c.Provider)
		}
		if !dnsProviderSpecs[typ].acme {
			return fmt.Errorf("dns certificate %q: provider %q (type %q) cannot issue certificates", c.Name, c.Provider, typ)
		}
	}

	zones := make(map[string]bool, len(d.Zones))
	for i, z := range d.Zones {
		if z.Domain == "" {
			return fmt.Errorf("dns zone[%d]: domain is required", i)
		}
		if zones[z.Domain] {
			return fmt.Errorf("dns zone %q: duplicate domain", z.Domain)
		}
		zones[z.Domain] = true
		typ, known := provTypes[z.Provider]
		if !known {
			return fmt.Errorf("dns zone %q: references unknown provider %q", z.Domain, z.Provider)
		}
		if !dnsProviderSpecs[typ].records {
			return fmt.Errorf("dns zone %q: provider %q (type %q) cannot manage records", z.Domain, z.Provider, typ)
		}
	}
	return nil
}

// ProviderByName returns the named provider, or nil when absent.
func (d *DNSConfig) ProviderByName(name string) *DNSProvider {
	if d == nil {
		return nil
	}
	for i := range d.Providers {
		if d.Providers[i].Name == name {
			return &d.Providers[i]
		}
	}
	return nil
}

// ZoneFor returns the most specific zone (longest matching Domain suffix) that
// governs domain, or nil when none — i.e. when Shuttle should not manage a record
// for it. Matching is on dot-bounded suffixes: zone "example.com" covers
// "app.example.com" and "example.com" itself, but not "notexample.com".
func (d *DNSConfig) ZoneFor(domain string) *DNSZone {
	if d == nil {
		return nil
	}
	var best *DNSZone
	for i := range d.Zones {
		z := &d.Zones[i]
		if domain == z.Domain || strings.HasSuffix(domain, "."+z.Domain) {
			if best == nil || len(z.Domain) > len(best.Domain) {
				best = z
			}
		}
	}
	return best
}

// CertificateNames returns the set of declared certificate names, for validating
// service pins.
func (d *DNSConfig) CertificateNames() map[string]bool {
	names := make(map[string]bool, len(d.Certificates))
	for _, c := range d.Certificates {
		names[c.Name] = true
	}
	return names
}

// DNSProviderCredentialKeys returns the credential keys a provider type requires
// (the map keys under a provider's `credentials:`), or nil for an unknown type.
// Backed by dnsProviderSpecs so the editor offers exactly what the type needs.
func DNSProviderCredentialKeys(providerType string) []string {
	spec, ok := dnsProviderSpecs[providerType]
	if !ok {
		return nil
	}
	return append([]string(nil), spec.requiredCreds...)
}

func supportedDNSTypes() string {
	return strings.Join(DNSProviderTypeNames(), ", ")
}

// DomainCoveredBy reports whether a domain falls under a certificate subject:
// an exact match, or a wildcard "*.zone" covering a single label ("host.zone",
// not the apex and not a deeper subdomain — matching Caddy's wildcard scope).
func DomainCoveredBy(domain, subject string) bool {
	if domain == subject {
		return true
	}
	if zone, ok := strings.CutPrefix(subject, "*."); ok {
		if rest, ok := strings.CutSuffix(domain, "."+zone); ok {
			return rest != "" && !strings.Contains(rest, ".")
		}
	}
	return false
}
