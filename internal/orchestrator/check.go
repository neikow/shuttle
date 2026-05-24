package orchestrator

import (
	"context"
	"fmt"
	"maps"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/secrets"
)

// CheckReport is the result of a configuration + secret-availability validation
// pass over the IaC repo. It collects every problem rather than failing on the
// first, so an operator sees the whole picture in one run.
type CheckReport struct {
	SHA            string
	GitCredentials []GitCredentialCheckResult
	Services       []ServiceCheck
}

// ServiceCheck is the per-service outcome of a Check. MissingKeys lists the
// env_schema keys absent from the resolved secrets; Err records a provider
// failure (e.g. an Infisical fetch error) that prevented the check.
type ServiceCheck struct {
	Service     string
	Env         string
	BasePath    string
	ServicePath string
	Schema      []string
	MissingKeys []string
	// Warnings are non-fatal advisories (e.g. a rolling-update service whose
	// compose can't run two instances at once). They don't fail the check.
	Warnings []string
	Err      error
}

// OK reports whether the service passed: no provider error and no missing keys.
// Warnings do not fail the check.
func (s ServiceCheck) OK() bool { return s.Err == nil && len(s.MissingKeys) == 0 }

// OK reports whether every service and git credential in the report passed.
func (r *CheckReport) OK() bool {
	for _, gc := range r.GitCredentials {
		if gc.Err != nil {
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

// GitCredentialCheckResult is the per-credential outcome of CheckGitCredentials.
type GitCredentialCheckResult struct {
	RepoPrefix string
	Key        string
	Err        error
}

// CheckGitCredentials verifies that every configured git credential's token
// key exists in the secrets provider. Returns one result per credential.
func (g *GitSyncer) CheckGitCredentials(ctx context.Context) []GitCredentialCheckResult {
	var results []GitCredentialCheckResult
	for _, cred := range g.gitCreds {
		r := GitCredentialCheckResult{RepoPrefix: cred.RepoPrefix, Key: cred.InfisicalKey}
		if g.secrets == nil {
			r.Err = fmt.Errorf("no secrets provider configured")
		} else {
			_, err := g.secrets.Get(ctx, secrets.Scope{Env: cred.InfisicalEnv, Path: cred.InfisicalPath}, cred.InfisicalKey)
			r.Err = err
		}
		results = append(results, r)
	}
	return results
}

// Check syncs the IaC repo, loads + validates its config (config.Load enforces
// referential integrity), and verifies that every key declared in each
// service's env_schema is present in the secrets provider. It mirrors
// renderEnv's resolution (shared base folder + service folder, service wins)
// but collects problems instead of failing fast, and never dispatches a deploy
// — so it is safe to run against a live system. With no secrets provider
// configured, the secret check is a no-op (env passthrough is off).
func (g *GitSyncer) Check(ctx context.Context) (*CheckReport, error) {
	repo, sha, err := g.syncAndLoad(ctx)
	if err != nil {
		return nil, err
	}
	report := &CheckReport{SHA: sha, GitCredentials: g.CheckGitCredentials(ctx)}
	for _, svc := range repo.Services {
		sc := g.checkService(ctx, svc)
		sc.Warnings = g.rollingCheck(ctx, svc)
		report.Services = append(report.Services, sc)
	}
	return report, nil
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

// checkService resolves a service's secrets and records which env_schema keys
// are missing. With no provider or no env_schema there is nothing to verify.
func (g *GitSyncer) checkService(ctx context.Context, svc config.Service) ServiceCheck {
	sc := ServiceCheck{Service: svc.Name, Env: svc.EnvFrom, Schema: svc.EnvSchema}
	if g.secrets == nil || len(svc.EnvSchema) == 0 {
		return sc
	}

	basePath, svcPath := config.ResolveSecretsPaths(g.secretsBasePath, g.secretsPathTemplate, svc.SecretPath, svc.Name)
	sc.BasePath, sc.ServicePath = basePath, svcPath

	all, err := g.secrets.GetAll(ctx, secrets.Scope{Env: svc.EnvFrom, Path: basePath})
	if err != nil {
		sc.Err = fmt.Errorf("secrets (base %q): %w", basePath, err)
		return sc
	}
	if svcPath != basePath {
		specific, err := g.secrets.GetAll(ctx, secrets.Scope{Env: svc.EnvFrom, Path: svcPath})
		if err != nil {
			sc.Err = fmt.Errorf("secrets (service %q): %w", svcPath, err)
			return sc
		}
		maps.Copy(all, specific) // service-specific keys override the shared base
	}

	for _, key := range svc.EnvSchema {
		if _, ok := all[key]; !ok {
			sc.MissingKeys = append(sc.MissingKeys, key)
		}
	}
	return sc
}
