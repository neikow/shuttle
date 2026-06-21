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
}

// DNSProvider is a named DNS-challenge provider. Type selects the Caddy DNS
// module (and thus the credential keys); Credentials map each provider field to
// a secret reference resolved from the secrets provider at config-build time and
// injected inline into the pushed Caddy config (never written to disk).
type DNSProvider struct {
	Name        string               `yaml:"name"`
	Type        string               `yaml:"type"`     // e.g. "ovh"
	Endpoint    string               `yaml:"endpoint"` // provider-specific (OVH: ovh-eu, ovh-ca, ...)
	Credentials map[string]SecretRef `yaml:"credentials"`
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

// dnsProviderSpec describes what a supported provider type requires. The
// required credential keys are also the Caddy DNS provider JSON field names, so
// the resolved credentials map straight onto the provider config.
type dnsProviderSpec struct {
	requiredCreds    []string
	endpointRequired bool
}

// dnsProviderSpecs is the registry of supported DNS provider types. A type is
// supported only when the shipped Caddy image (shuttle-caddy) bundles its plugin
// — add the plugin and the spec together.
var dnsProviderSpecs = map[string]dnsProviderSpec{
	"ovh": {requiredCreds: []string{"application_key", "application_secret", "consumer_key"}, endpointRequired: true},
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
	providers := make(map[string]bool, len(d.Providers))
	for i, p := range d.Providers {
		if p.Name == "" {
			return fmt.Errorf("dns provider[%d]: name is required", i)
		}
		if providers[p.Name] {
			return fmt.Errorf("dns provider %q: duplicate name", p.Name)
		}
		providers[p.Name] = true

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
		if !providers[c.Provider] {
			return fmt.Errorf("dns certificate %q: references unknown provider %q", c.Name, c.Provider)
		}
	}
	return nil
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

func supportedDNSTypes() string {
	types := make([]string, 0, len(dnsProviderSpecs))
	for t := range dnsProviderSpecs {
		types = append(types, t)
	}
	return strings.Join(types, ", ")
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
