package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/secrets"
)

// SecretPoller periodically fingerprints the Infisical folders the repo's
// services read and force-redeploys any service whose secret set changed. It is
// a fallback for environments where Infisical webhooks are not delivered.
//
// Security: the poller never persists secret values. For each (env, folder) it
// keeps only a SHA-256 fingerprint of the resolved key/value set; a change is a
// fingerprint mismatch. Plaintext exists only transiently inside the GetAll
// call. Fingerprints live in memory, so a restart re-seeds them (no redeploy on
// the first pass).
type SecretPoller struct {
	syncer     *GitSyncer
	defaultEnv string
	interval   time.Duration

	mu     sync.Mutex
	fps    map[SecretChange]string // (env, folder) -> last fingerprint
	seeded bool
}

// NewSecretPoller builds a poller over the syncer's secrets provider. defaultEnv
// resolves services with no env_from (matching renderEnv / the webhook path).
func NewSecretPoller(syncer *GitSyncer, interval time.Duration, defaultEnv string) *SecretPoller {
	return &SecretPoller{
		syncer:     syncer,
		defaultEnv: defaultEnv,
		interval:   interval,
		fps:        make(map[SecretChange]string),
	}
}

// Run polls until ctx is cancelled. The first pass only seeds fingerprints (no
// redeploy), so a restart does not trigger a redeploy storm.
func (p *SecretPoller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.tick(ctx); err != nil {
				slog.Error("secret poll failed", "err", err)
			}
		}
	}
}

func (p *SecretPoller) tick(ctx context.Context) error {
	// Load the working copy as last synced by the drift reconciler; the poller
	// does no git work itself. If the repo is not yet present, skip this tick.
	repo, err := config.Load(p.syncer.LocalDir())
	if err != nil {
		return fmt.Errorf("load repo: %w", err)
	}

	changed := p.diffScopes(ctx, p.repoScopes(repo))
	if len(changed) == 0 {
		return nil
	}

	affected := make(map[string]struct{})
	for _, c := range changed {
		for _, svc := range p.syncer.servicesMatching(repo.Services, c.Env, c.Path, p.defaultEnv) {
			affected[svc] = struct{}{}
		}
	}
	if len(affected) == 0 {
		return nil
	}
	services := make([]string, 0, len(affected))
	for s := range affected {
		services = append(services, s)
	}
	sort.Strings(services)

	slog.Info("secret poll: change detected, redeploying", "scopes", len(changed), "services", services)
	if _, err := p.syncer.ForceDeploy(ctx, services); err != nil {
		return fmt.Errorf("force redeploy: %w", err)
	}
	return nil
}

// repoScopes returns the distinct (env, folder) secret scopes the repo's
// services read — each service's shared base folder and its own folder, in the
// service's effective environment.
func (p *SecretPoller) repoScopes(repo *config.Repo) []SecretChange {
	seen := make(map[SecretChange]struct{})
	var out []SecretChange
	add := func(env, path string) {
		c := SecretChange{Env: env, Path: path}
		if _, ok := seen[c]; ok {
			return
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	for _, svc := range repo.Services {
		env := svc.EnvFrom
		if env == "" {
			env = p.defaultEnv
		}
		base, svcPath := config.ResolveSecretsPaths(p.syncer.secretsBasePath, p.syncer.secretsPathTemplate, svc.SecretPath, svc.Name)
		add(env, base)
		add(env, svcPath)
	}
	return out
}

// diffScopes fetches each scope's secrets, fingerprints them, and returns the
// scopes whose fingerprint changed since the last poll. The first poll seeds all
// fingerprints and reports nothing; a scope seen for the first time later (e.g.
// a newly added service) is likewise only seeded — its initial deploy is handled
// by the normal SHA reconcile. Secret values are not retained.
func (p *SecretPoller) diffScopes(ctx context.Context, scopes []SecretChange) []SecretChange {
	p.mu.Lock()
	defer p.mu.Unlock()

	var changed []SecretChange
	for _, c := range scopes {
		all, err := p.syncer.secrets.GetAll(ctx, secrets.Scope{Env: c.Env, Path: c.Path})
		if err != nil {
			slog.Warn("secret poll: fetch failed", "env", c.Env, "path", c.Path, "err", err)
			continue
		}
		fp := fingerprintSecrets(all)
		prev, known := p.fps[c]
		p.fps[c] = fp
		if p.seeded && known && prev != fp {
			changed = append(changed, c)
		}
	}
	p.seeded = true
	return changed
}

// fingerprintSecrets returns a SHA-256 over the sorted key/value set. The NUL
// separators make the encoding unambiguous so distinct sets cannot collide via
// concatenation. The plaintext is consumed here and never stored.
func fingerprintSecrets(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(m[k]))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
