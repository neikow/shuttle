package orchestrator

import (
	"context"
	"fmt"
	"maps"
	"os"
	"sort"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/secrets"
)

// CheckReport is the result of a configuration + secret-availability validation
// pass over the IaC repo. It collects every problem rather than failing on the
// first, so an operator sees the whole picture in one run.
type CheckReport struct {
	SHA            string                     `json:"sha"`
	HasProvider    bool                       `json:"has_provider"`
	GitCredentials []GitCredentialCheckResult `json:"git_credentials,omitempty"`
	DNSProviders   []DNSCredentialCheckResult `json:"dns_providers,omitempty"`
	Services       []ServiceCheck             `json:"services,omitempty"`
}

// ServiceCheck is the per-service outcome of a Check. Schema lists the declared
// `env:` variable names; MissingKeys lists the env references that don't resolve
// (a provider key absent from the secrets, or an unset ${env:KEY}); Err records
// a provider failure (e.g. an Infisical fetch error) that prevented the check.
type ServiceCheck struct {
	Service     string   `json:"service"`
	Env         string   `json:"env,omitempty"`
	BasePath    string   `json:"base_path,omitempty"`
	ServicePath string   `json:"service_path,omitempty"`
	Schema      []string `json:"schema,omitempty"`
	MissingKeys []string `json:"missing_keys,omitempty"`
	// Warnings are non-fatal advisories (e.g. a rolling-update service whose
	// compose can't run two instances at once). They don't fail the check.
	Warnings []string `json:"warnings,omitempty"`
	Err      string   `json:"error,omitempty"`
}

// OK reports whether the service passed: no provider error and no missing keys.
// Warnings do not fail the check.
func (s ServiceCheck) OK() bool { return s.Err == "" && len(s.MissingKeys) == 0 }

// OK reports whether every service, git credential, and DNS provider in the
// report passed.
func (r *CheckReport) OK() bool {
	for _, gc := range r.GitCredentials {
		if gc.Err != "" {
			return false
		}
	}
	for _, dp := range r.DNSProviders {
		if dp.Err != "" {
			return false
		}
	}
	for _, s := range r.Services {
		if !s.OK() {
			return false
		}
	}
	return true
}

// DNSCredentialCheckResult is the per-provider outcome of CheckDNSCredentials:
// whether every credential the DNS provider declares resolves in the secrets
// provider. Err names the first failing credential.
type DNSCredentialCheckResult struct {
	Provider string `json:"provider"`
	Type     string `json:"type"`
	Err      string `json:"error,omitempty"`
}

// CheckDNSCredentials verifies that every dns.yml provider's credential
// references resolve in the secrets provider. Returns one result per provider
// (nil when the repo has no dns.yml).
func (g *GitSyncer) CheckDNSCredentials(ctx context.Context, repo *config.Repo) []DNSCredentialCheckResult {
	if repo.DNS == nil {
		return nil
	}
	var results []DNSCredentialCheckResult
	for _, p := range repo.DNS.Providers {
		r := DNSCredentialCheckResult{Provider: p.Name, Type: p.Type}
		switch {
		case len(p.Credentials) > 0 && g.secrets == nil:
			r.Err = "no secrets provider configured"
		default:
			for field, ref := range p.Credentials {
				if _, err := g.secrets.Get(ctx, secrets.Scope{Env: ref.InfisicalEnv, Path: ref.InfisicalPath}, ref.InfisicalKey); err != nil {
					r.Err = fmt.Sprintf("credential %q (%s): %v", field, ref.InfisicalKey, err)
					break
				}
			}
		}
		results = append(results, r)
	}
	return results
}

// GitCredentialCheckResult is the per-credential outcome of CheckGitCredentials.
type GitCredentialCheckResult struct {
	RepoPrefix string `json:"repo_prefix"`
	Key        string `json:"key"`
	Err        string `json:"error,omitempty"`
}

// CheckGitCredentials verifies that every configured git credential's token
// key exists in the secrets provider. Returns one result per credential.
func (g *GitSyncer) CheckGitCredentials(ctx context.Context) []GitCredentialCheckResult {
	var results []GitCredentialCheckResult
	for _, cred := range g.gitCreds {
		r := GitCredentialCheckResult{RepoPrefix: cred.RepoPrefix, Key: cred.InfisicalKey}
		if g.secrets == nil {
			r.Err = "no secrets provider configured"
		} else if _, err := g.secrets.Get(ctx, secrets.Scope{Env: cred.InfisicalEnv, Path: cred.InfisicalPath}, cred.InfisicalKey); err != nil {
			r.Err = err.Error()
		}
		results = append(results, r)
	}
	return results
}

// Check syncs the IaC repo, loads + validates its config (config.Load enforces
// referential integrity), and verifies that every reference in each service's
// env: map resolves (provider secret or ${env:KEY}). It mirrors
// renderEnv's resolution (shared base folder + service folder, service wins)
// but collects problems instead of failing fast, and never dispatches a deploy
// — so it is safe to run against a live system. With no secrets provider
// configured, the secret check is a no-op (env passthrough is off).
func (g *GitSyncer) Check(ctx context.Context) (*CheckReport, error) {
	repo, sha, err := g.syncAndLoad(ctx)
	if err != nil {
		return nil, err
	}
	return g.checkRepo(ctx, repo, sha), nil
}

// CheckRef is Check against an arbitrary git ref (branch, tag, refs/pull/N/head,
// or SHA) instead of the configured branch HEAD, so CI can validate the exact PR
// branch. ref == "" falls back to Check. The ref is checked out in isolation,
// leaving the orchestrator's working tree intact.
func (g *GitSyncer) CheckRef(ctx context.Context, ref string) (*CheckReport, error) {
	if ref == "" {
		return g.Check(ctx)
	}
	sib, cleanup, err := g.checkoutRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	repo, sha, err := sib.loadHead(ctx)
	if err != nil {
		return nil, err
	}
	// Render/validate from the sibling's tree (renderCompose reads its dir).
	return sib.checkRepo(ctx, repo, sha), nil
}

// checkRepo validates an already-loaded repo+SHA: git-credential availability,
// per-service env resolution, and rolling-update warnings.
func (g *GitSyncer) checkRepo(ctx context.Context, repo *config.Repo, sha string) *CheckReport {
	report := &CheckReport{
		SHA:            sha,
		HasProvider:    g.secrets != nil,
		GitCredentials: g.CheckGitCredentials(ctx),
		DNSProviders:   g.CheckDNSCredentials(ctx, repo),
	}
	for _, svc := range repo.Services {
		if svc.IsExternal() {
			continue // proxy-only: no compose/env to validate
		}
		sc := g.checkService(ctx, svc)
		sc.Warnings = g.rollingCheck(ctx, svc)
		report.Services = append(report.Services, sc)
	}
	return report
}

// rollingCheck warns when a service using the rolling update policy has a
// compose file that cannot run two instances at once. Only rolling services are
// inspected; a compose that can't be fetched/rendered is skipped (the deploy
// path reports that error).
func (g *GitSyncer) rollingCheck(ctx context.Context, svc config.Service) []string {
	if svc.UpdatePolicy == config.UpdatePolicyRecreate {
		return nil
	}
	composeYAML, err := g.renderCompose(ctx, svc)
	if err != nil {
		return nil
	}
	return rollingWarnings(composeYAML)
}

// checkService resolves a service's `env:` map and records which references
// don't resolve. With no env, nothing to verify. The provider folders are only
// fetched when a value references the provider — so a service that reads no
// provider secrets never requires its folder to exist.
func (g *GitSyncer) checkService(ctx context.Context, svc config.Service) ServiceCheck {
	sc := ServiceCheck{Service: svc.Name, Env: svc.EnvFrom, Schema: sortedKeys(svc.Env)}
	if len(svc.Env) == 0 {
		return sc
	}

	var provider map[string]string
	if envUsesProvider(svc.Env) {
		if g.secrets == nil {
			sc.Err = "env references provider secrets but no secrets_provider is configured"
			return sc
		}
		basePath, svcPath := config.ResolveSecretsPaths(g.secretsBasePath, g.secretsPathTemplate, svc.SecretPath, svc.Name)
		sc.BasePath, sc.ServicePath = basePath, svcPath

		all, err := g.secrets.GetAll(ctx, secrets.Scope{Env: svc.EnvFrom, Path: basePath})
		if err != nil {
			sc.Err = fmt.Sprintf("secrets (base %q): %v", basePath, err)
			return sc
		}
		if svcPath != basePath {
			specific, err := g.secrets.GetAll(ctx, secrets.Scope{Env: svc.EnvFrom, Path: svcPath})
			if err != nil {
				sc.Err = fmt.Sprintf("secrets (service %q): %v", svcPath, err)
				return sc
			}
			maps.Copy(all, specific) // service-specific keys override the shared base
		}
		provider = all
	}

	if _, missing := resolveEnv(svc.Env, provider, os.LookupEnv); len(missing) > 0 {
		for _, r := range missing {
			sc.MissingKeys = append(sc.MissingKeys, r.String())
		}
	}
	return sc
}

// sortedKeys returns the map's keys in sorted order (deterministic output).
func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
