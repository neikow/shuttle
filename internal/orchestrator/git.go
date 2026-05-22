package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/ledger"
	"github.com/neikow/shuttle/internal/secrets"
)

// GitSyncer clones/pulls the IaC repo, diffs it against the ledger, and
// dispatches deploy jobs to connected agents. It shells out to the git binary
// (consistent with the agent's docker-compose shell-out) to avoid a heavy
// go-git dependency.
type GitSyncer struct {
	repoURL  string
	branch   string
	dir      string
	store    *ledger.Store
	registry *Registry
	secrets  secrets.Provider
	caddy    *CaddyClient // optional; when set, routes are pushed on each reconcile
}

// SetCaddy attaches a Caddy admin client; routes derived from the repo are
// pushed after each reconcile. Call before serving.
func (g *GitSyncer) SetCaddy(c *CaddyClient) { g.caddy = c }

func NewGitSyncer(repoURL, branch, dir string, store *ledger.Store, registry *Registry, sec secrets.Provider) *GitSyncer {
	if branch == "" {
		branch = "main"
	}
	return &GitSyncer{
		repoURL:  repoURL,
		branch:   branch,
		dir:      dir,
		store:    store,
		registry: registry,
		secrets:  sec,
	}
}

// Sync clones the repo if absent, otherwise fetches and hard-resets to the tip
// of the configured branch. Returns the checked-out commit SHA.
func (g *GitSyncer) Sync(ctx context.Context) (string, error) {
	if _, err := os.Stat(filepath.Join(g.dir, ".git")); errors.Is(err, os.ErrNotExist) {
		if err := g.git(ctx, "", "clone", "--branch", g.branch, "--single-branch", g.repoURL, g.dir); err != nil {
			return "", fmt.Errorf("clone: %w", err)
		}
	} else {
		if err := g.git(ctx, g.dir, "fetch", "origin", g.branch); err != nil {
			return "", fmt.Errorf("fetch: %w", err)
		}
		if err := g.git(ctx, g.dir, "reset", "--hard", "origin/"+g.branch); err != nil {
			return "", fmt.Errorf("reset: %w", err)
		}
	}
	sha, err := g.gitOut(ctx, g.dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("rev-parse: %w", err)
	}
	return strings.TrimSpace(sha), nil
}

// syncAndLoad syncs the repo and parses the IaC config, returning the parsed
// repo and the checked-out SHA.
func (g *GitSyncer) syncAndLoad(ctx context.Context) (*config.Repo, string, error) {
	sha, err := g.Sync(ctx)
	if err != nil {
		return nil, "", err
	}
	repo, err := config.Load(g.dir)
	if err != nil {
		return nil, "", fmt.Errorf("load config: %w", err)
	}
	return repo, sha, nil
}

// dispatchPlan dispatches the deploy steps, skipping any not in filter (when
// filter is non-empty). Returns the dispatched deploy IDs.
func (g *GitSyncer) dispatchPlan(ctx context.Context, repo *config.Repo, steps []Step, filter map[string]bool) []string {
	svcByName := make(map[string]config.Service, len(repo.Services))
	for _, s := range repo.Services {
		svcByName[s.Name] = s
	}

	var dispatched []string
	for _, step := range steps {
		if step.Action != ActionDeploy {
			continue
		}
		if len(filter) > 0 && !filter[step.Service] {
			continue
		}
		deployID, err := g.dispatch(ctx, svcByName[step.Service], step, ledger.TriggeredByWebhook)
		if err != nil {
			slog.Error("dispatch failed", "service", step.Service, "host", step.Host, "err", err)
			continue
		}
		dispatched = append(dispatched, deployID)
	}
	return dispatched
}

// Reconcile syncs the repo, computes the deploy plan against the ledger, and
// dispatches deploys. If onlyServices is non-empty, only those services are
// considered. Returns the deploy IDs that were dispatched.
func (g *GitSyncer) Reconcile(ctx context.Context, onlyServices []string) ([]string, error) {
	repo, sha, err := g.syncAndLoad(ctx)
	if err != nil {
		return nil, err
	}
	g.applyRoutes(ctx, repo)
	current, err := g.store.CurrentSHAs(ctx)
	if err != nil {
		return nil, fmt.Errorf("current state: %w", err)
	}
	plan := ComputePlan(repo, CurrentState(current), sha)
	return g.dispatchPlan(ctx, repo, plan.Steps, toSet(onlyServices)), nil
}

// applyRoutes pushes the repo's desired routes to Caddy when configured.
func (g *GitSyncer) applyRoutes(ctx context.Context, repo *config.Repo) {
	if g.caddy == nil {
		return
	}
	routes := RoutesFromRepo(repo)
	if err := g.caddy.ApplyRoutes(ctx, routes); err != nil {
		slog.Error("apply caddy routes failed", "err", err)
		return
	}
	slog.Info("caddy routes applied", "count", len(routes))
}

// ForceDeploy redeploys the named services at the current repo HEAD regardless
// of ledger state. Used by the drift reconciler to recover crashed containers.
func (g *GitSyncer) ForceDeploy(ctx context.Context, services []string) ([]string, error) {
	repo, sha, err := g.syncAndLoad(ctx)
	if err != nil {
		return nil, err
	}
	steps := make([]Step, 0, len(repo.Services))
	for _, svc := range repo.Services {
		steps = append(steps, Step{Host: svc.Host, Service: svc.Name, Action: ActionDeploy, SHA: sha})
	}
	return g.dispatchPlan(ctx, repo, steps, toSet(services)), nil
}

// LocalDir returns the path of the synced working copy.
func (g *GitSyncer) LocalDir() string { return g.dir }

// DeployAtSHA checks out the repo at sha, renders the named service's compose +
// env at that revision, and dispatches a deploy. Used by the manual deploy and
// rollback HTTP endpoints, which must ship real compose (unlike a bare
// DeployRequest). Returns the deploy ID and the resolved host.
//
// The working copy is left detached at sha; the next Reconcile resets it to the
// branch tip.
func (g *GitSyncer) DeployAtSHA(ctx context.Context, service, sha string, triggeredBy ledger.TriggeredBy) (deployID, host string, err error) {
	// Ensure the repo (and its history) is present.
	if _, statErr := os.Stat(filepath.Join(g.dir, ".git")); errors.Is(statErr, os.ErrNotExist) {
		if _, syncErr := g.Sync(ctx); syncErr != nil {
			return "", "", syncErr
		}
	} else if fetchErr := g.git(ctx, g.dir, "fetch", "origin", g.branch); fetchErr != nil {
		return "", "", fmt.Errorf("fetch: %w", fetchErr)
	}
	if coErr := g.git(ctx, g.dir, "checkout", sha); coErr != nil {
		return "", "", fmt.Errorf("checkout %s: %w", sha, coErr)
	}

	repo, loadErr := config.Load(g.dir)
	if loadErr != nil {
		return "", "", fmt.Errorf("load config at %s: %w", sha, loadErr)
	}
	var svc *config.Service
	for i := range repo.Services {
		if repo.Services[i].Name == service {
			svc = &repo.Services[i]
			break
		}
	}
	if svc == nil {
		return "", "", fmt.Errorf("service %q not found at %s", service, sha)
	}

	step := Step{Host: svc.Host, Service: svc.Name, Action: ActionDeploy, SHA: sha}
	id, dispErr := g.dispatch(ctx, *svc, step, triggeredBy)
	if dispErr != nil {
		return "", "", dispErr
	}
	return id, svc.Host, nil
}

func (g *GitSyncer) dispatch(ctx context.Context, svc config.Service, step Step, triggeredBy ledger.TriggeredBy) (string, error) {
	composeYAML, err := g.renderCompose(ctx, svc)
	if err != nil {
		return "", err
	}
	env, err := g.renderEnv(ctx, svc)
	if err != nil {
		return "", err
	}

	deployID := newID()
	rec := ledger.DeployRecord{
		DeployID:    deployID,
		Service:     step.Service,
		Host:        step.Host,
		SHA:         step.SHA,
		Status:      ledger.StatusPending,
		TriggeredBy: triggeredBy,
		StartedAt:   time.Now(),
	}
	if err := g.store.RecordDeploy(ctx, rec); err != nil {
		return "", fmt.Errorf("record deploy: %w", err)
	}

	cmd := &shuttlev1.OrchestratorCommand{
		Payload: &shuttlev1.OrchestratorCommand_Deploy{
			Deploy: &shuttlev1.DeployRequest{
				DeployId:    deployID,
				Service:     step.Service,
				Sha:         step.SHA,
				Env:         env,
				ComposeYaml: composeYAML,
			},
		},
	}
	if err := g.registry.Send(step.Host, cmd); err != nil {
		_ = g.store.MarkStatus(ctx, deployID, ledger.StatusFailed)
		return "", fmt.Errorf("send to agent: %w", err)
	}
	slog.Info("deploy dispatched", "deploy_id", deployID, "service", step.Service, "host", step.Host, "sha", step.SHA)
	return deployID, nil
}

// renderCompose returns the service's compose YAML, either from the local repo
// or fetched from a remote git pointer.
func (g *GitSyncer) renderCompose(ctx context.Context, svc config.Service) ([]byte, error) {
	switch src := svc.Source.(type) {
	case config.LocalCompose:
		path := filepath.Join(g.dir, src.Path)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read compose %s: %w", path, err)
		}
		return data, nil
	case config.RemotePointer:
		return g.fetchRemoteCompose(ctx, src)
	default:
		return nil, fmt.Errorf("service %q: unknown compose source %T", svc.Name, svc.Source)
	}
}

// fetchRemoteCompose shallow-clones (or updates) the pointer's repo into a
// cache beside the working copy and reads the referenced compose file.
func (g *GitSyncer) fetchRemoteCompose(ctx context.Context, rp config.RemotePointer) ([]byte, error) {
	branch := rp.Branch
	if branch == "" {
		branch = "main"
	}
	cacheDir := filepath.Join(g.dir+".remotes", sanitizeRepoKey(rp.Repo))
	if _, err := os.Stat(filepath.Join(cacheDir, ".git")); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(cacheDir), 0o755); err != nil {
			return nil, fmt.Errorf("mkdir remote cache: %w", err)
		}
		if err := g.git(ctx, "", "clone", "--branch", branch, "--single-branch", "--depth", "1", rp.Repo, cacheDir); err != nil {
			return nil, fmt.Errorf("clone remote %s: %w", rp.Repo, err)
		}
	} else {
		if err := g.git(ctx, cacheDir, "fetch", "--depth", "1", "origin", branch); err != nil {
			return nil, fmt.Errorf("fetch remote %s: %w", rp.Repo, err)
		}
		if err := g.git(ctx, cacheDir, "reset", "--hard", "origin/"+branch); err != nil {
			return nil, fmt.Errorf("reset remote %s: %w", rp.Repo, err)
		}
	}

	path := filepath.Join(cacheDir, rp.Path)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read remote compose %s: %w", path, err)
	}
	return data, nil
}

// sanitizeRepoKey turns a repo URL into a filesystem-safe cache directory name.
func sanitizeRepoKey(repo string) string {
	var b strings.Builder
	for _, r := range repo {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// renderEnv resolves the service's secrets. When EnvSchema is set only those
// keys are included; otherwise all secrets are passed through.
func (g *GitSyncer) renderEnv(ctx context.Context, svc config.Service) (map[string]string, error) {
	if g.secrets == nil {
		return nil, nil
	}
	all, err := g.secrets.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("secrets: %w", err)
	}
	if len(svc.EnvSchema) == 0 {
		return all, nil
	}
	env := make(map[string]string, len(svc.EnvSchema))
	for _, key := range svc.EnvSchema {
		if v, ok := all[key]; ok {
			env[key] = v
		} else {
			slog.Warn("env key declared in schema but not in secrets", "service", svc.Name, "key", key)
		}
	}
	return env, nil
}

func (g *GitSyncer) git(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (g *GitSyncer) gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	return string(out), err
}

func toSet(items []string) map[string]bool {
	if len(items) == 0 {
		return nil
	}
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}
