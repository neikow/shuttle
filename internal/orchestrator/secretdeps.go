package orchestrator

import (
	"context"
	"sort"
	"strings"

	"github.com/neikow/shuttle/internal/config"
)

// ServicesUsingSecret syncs the repo and returns the names of services whose
// secrets are read from environment env at a folder overlapping changedPath —
// i.e. the services that must be redeployed when that Infisical secret changes.
// A service's effective environment is its env_from, or defaultEnv when unset
// (matching renderEnv's resolution against INFISICAL_ENV). The result is sorted
// for deterministic dispatch and logging.
func (g *GitSyncer) ServicesUsingSecret(ctx context.Context, env, changedPath, defaultEnv string) ([]string, error) {
	repo, _, err := g.syncAndLoad(ctx)
	if err != nil {
		return nil, err
	}
	return g.servicesMatching(repo.Services, env, changedPath, defaultEnv), nil
}

// servicesMatching is the pure mapping from a changed (env, folder) to the
// services that read it, given the syncer's secrets path config.
func (g *GitSyncer) servicesMatching(services []config.Service, env, changedPath, defaultEnv string) []string {
	var affected []string
	for _, svc := range services {
		effEnv := svc.EnvFrom
		if effEnv == "" {
			effEnv = defaultEnv
		}
		if effEnv != env {
			continue
		}
		base, svcPath := config.ResolveSecretsPaths(g.secretsBasePath, g.secretsPathTemplate, svc.SecretPath, svc.Name)
		if sameFolder(changedPath, base) || sameFolder(changedPath, svcPath) {
			affected = append(affected, svc.Name)
		}
	}
	sort.Strings(affected)
	return affected
}

// sameFolder reports whether two Infisical folder paths reference the same
// folder, ignoring a trailing slash. renderEnv reads each service's folders
// non-recursively, so only an exact folder match means the change affects it.
func sameFolder(a, b string) bool {
	return normalizeFolder(a) == normalizeFolder(b)
}

// normalizeFolder trims a trailing slash so "/a/" and "/a" compare equal; the
// root "/" is preserved.
func normalizeFolder(p string) string {
	if p == "" {
		return "/"
	}
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	return p
}
