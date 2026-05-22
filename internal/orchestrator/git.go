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
}

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

// Reconcile syncs the repo, computes the deploy plan against the ledger, and
// dispatches deploys. If onlyServices is non-empty, only those services are
// considered. Returns the deploy IDs that were dispatched.
func (g *GitSyncer) Reconcile(ctx context.Context, onlyServices []string) ([]string, error) {
	sha, err := g.Sync(ctx)
	if err != nil {
		return nil, err
	}

	repo, err := config.Load(g.dir)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	current, err := g.store.CurrentSHAs(ctx)
	if err != nil {
		return nil, fmt.Errorf("current state: %w", err)
	}

	filter := toSet(onlyServices)
	plan := ComputePlan(repo, CurrentState(current), sha)

	svcByName := make(map[string]config.Service, len(repo.Services))
	for _, s := range repo.Services {
		svcByName[s.Name] = s
	}

	var dispatched []string
	for _, step := range plan.Steps {
		if step.Action != ActionDeploy {
			continue
		}
		if len(filter) > 0 && !filter[step.Service] {
			continue
		}
		deployID, err := g.dispatch(ctx, svcByName[step.Service], step)
		if err != nil {
			slog.Error("dispatch failed", "service", step.Service, "host", step.Host, "err", err)
			continue
		}
		dispatched = append(dispatched, deployID)
	}
	return dispatched, nil
}

func (g *GitSyncer) dispatch(ctx context.Context, svc config.Service, step Step) (string, error) {
	composeYAML, err := g.renderCompose(svc)
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
		TriggeredBy: ledger.TriggeredByWebhook,
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
		g.store.MarkStatus(ctx, deployID, ledger.StatusFailed)
		return "", fmt.Errorf("send to agent: %w", err)
	}
	slog.Info("deploy dispatched", "deploy_id", deployID, "service", step.Service, "host", step.Host, "sha", step.SHA)
	return deployID, nil
}

// renderCompose reads the service's local compose file. Remote pointers are not
// yet supported by the sync loop.
func (g *GitSyncer) renderCompose(svc config.Service) ([]byte, error) {
	local, ok := svc.Source.(config.LocalCompose)
	if !ok {
		return nil, fmt.Errorf("service %q: remote compose sources not yet supported", svc.Name)
	}
	path := filepath.Join(g.dir, local.Path)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read compose %s: %w", path, err)
	}
	return data, nil
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
