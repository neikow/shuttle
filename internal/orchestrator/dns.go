package orchestrator

import (
	"context"
	"fmt"
	"sort"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/secrets"
)

// resolveTLSPolicies builds the Caddy TLS automation policies for the DNS-managed
// certificates relevant to a host. A certificate is relevant when the host
// serves a domain it covers (exact or wildcard) or a service on the host pins it
// (tls_certificate). host=="" matches every service — used by the non-host-scoped
// central Caddy.
//
// Provider credentials are fetched fresh from the secrets provider and injected
// inline into the returned policy objects; the config is pushed to the agent over
// the TLS stream and reloaded from stdin, so secrets never touch disk or argv
// (mirroring how deploy env secrets flow). Returns nil when the repo declares no
// certificates, leaving Caddy's default automation (per-domain HTTP-01) in place.
func (g *GitSyncer) resolveTLSPolicies(ctx context.Context, repo *config.Repo, host string) ([]map[string]any, error) {
	if repo.DNS == nil || len(repo.DNS.Certificates) == 0 {
		return nil, nil
	}
	provByName := make(map[string]config.DNSProvider, len(repo.DNS.Providers))
	for _, p := range repo.DNS.Providers {
		provByName[p.Name] = p
	}

	// Domains this host serves, plus the pin domains contributed per certificate.
	served := map[string]bool{}
	pinsByCert := map[string][]string{}
	for _, svc := range repo.Services {
		if host != "" && svc.Host != host {
			continue
		}
		for _, d := range svc.Domains {
			served[d] = true
		}
		if svc.TLSCertificate != "" {
			pinsByCert[svc.TLSCertificate] = append(pinsByCert[svc.TLSCertificate], svc.Domains...)
		}
	}

	credCache := map[string]map[string]string{}
	var policies []map[string]any
	for _, cert := range repo.DNS.Certificates {
		subjects := map[string]bool{}
		relevant := false
		for d := range served {
			for _, sub := range cert.Domains {
				if config.DomainCoveredBy(d, sub) {
					relevant = true
				}
			}
		}
		if pinned, ok := pinsByCert[cert.Name]; ok {
			relevant = true
			for _, d := range pinned {
				subjects[d] = true
			}
		}
		if !relevant {
			continue
		}
		for _, sub := range cert.Domains {
			subjects[sub] = true
		}

		prov := provByName[cert.Provider] // validated to exist at load
		creds, ok := credCache[prov.Name]
		if !ok {
			var err error
			creds, err = g.resolveDNSCreds(ctx, prov)
			if err != nil {
				return nil, err
			}
			credCache[prov.Name] = creds
		}
		policies = append(policies, caddyTLSPolicy(sortedStringSet(subjects), caddyDNSProvider(prov, creds)))
	}
	return policies, nil
}

// resolveDNSCreds fetches a provider's credential values from the secrets
// provider, keyed by the Caddy provider field name.
func (g *GitSyncer) resolveDNSCreds(ctx context.Context, p config.DNSProvider) (map[string]string, error) {
	if len(p.Credentials) > 0 && g.secrets == nil {
		return nil, fmt.Errorf("dns provider %q has credentials but no secrets provider configured", p.Name)
	}
	out := make(map[string]string, len(p.Credentials))
	for field, ref := range p.Credentials {
		v, err := g.secrets.Get(ctx, secrets.Scope{Env: ref.InfisicalEnv, Path: ref.InfisicalPath}, ref.InfisicalKey)
		if err != nil {
			return nil, fmt.Errorf("dns provider %q credential %q: %w", p.Name, field, err)
		}
		out[field] = v
	}
	return out, nil
}

// caddyDNSProvider shapes a resolved provider into Caddy's dns provider config
// object: {name:<type>, endpoint:<endpoint>, <credential fields...>}. The cred
// map keys are the Caddy provider's JSON field names (e.g. application_key).
func caddyDNSProvider(p config.DNSProvider, creds map[string]string) map[string]any {
	obj := map[string]any{"name": p.Type}
	if p.Endpoint != "" {
		obj["endpoint"] = p.Endpoint
	}
	for k, v := range creds {
		obj[k] = v
	}
	return obj
}

// caddyTLSPolicy builds one TLS automation policy: issue subjects via the
// provider's ACME DNS-01 challenge (the only challenge that can mint wildcards).
func caddyTLSPolicy(subjects []string, provider map[string]any) map[string]any {
	return map[string]any{
		"subjects": subjects,
		"issuers": []any{
			map[string]any{
				"module": "acme",
				"challenges": map[string]any{
					"dns": map[string]any{"provider": provider},
				},
			},
		},
	}
}

func sortedStringSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
