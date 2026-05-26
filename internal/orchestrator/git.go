package orchestrator

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"maps"
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
	// httpsRedirect, when true, makes Caddy serve only :443 and auto-redirect
	// :80 -> HTTPS (308). When false, :80 is served plaintext (no redirect).
	httpsRedirect bool
	// secretsBasePath is the shared secrets folder merged under every service;
	// secretsPathTemplate derives a service's own folder from its name. Both feed
	// renderEnv via config.ResolveSecretsPaths.
	secretsBasePath     string
	secretsPathTemplate string
	gitCreds            []config.GitCredential
	bus                 *EventBus // optional; nil-safe
}

// SetEventBus attaches the event bus this syncer publishes to. Call before serving.
func (g *GitSyncer) SetEventBus(b *EventBus) { g.bus = b }

// SetCaddy attaches a Caddy admin client; routes derived from the repo are
// pushed after each reconcile. Call before serving.
func (g *GitSyncer) SetCaddy(c *CaddyClient) { g.caddy = c }

// SetHTTPSRedirect toggles HTTP->HTTPS redirect in the generated Caddy config.
func (g *GitSyncer) SetHTTPSRedirect(v bool) { g.httpsRedirect = v }

// SetSecretsPaths configures the shared base folder and per-service path template
// used to resolve where each service's secrets are read from. Call before serving.
func (g *GitSyncer) SetSecretsPaths(basePath, template string) {
	g.secretsBasePath = basePath
	g.secretsPathTemplate = template
}

// SetGitCredentials attaches per-host/org HTTPS token credentials used to
// authenticate git operations against private repos. Call before serving.
func (g *GitSyncer) SetGitCredentials(creds []config.GitCredential) {
	g.gitCreds = creds
}

// credEnv returns the process environment that authenticates a git operation
// against rawURL when a matching GitCredential is configured, or nil if there is
// no match (or no secrets provider). The token is injected as a one-shot
// http.extraHeader via GIT_CONFIG_* env vars rather than embedded in the remote
// URL, so it is never written to the clone's .git/config on disk and never
// appears in the process argument list. The header is scoped to the
// credential's repo prefix so it is not sent to any other remote a single
// command might contact.
func (g *GitSyncer) credEnv(ctx context.Context, rawURL string) ([]string, error) {
	if g.secrets == nil || len(g.gitCreds) == 0 {
		return nil, nil
	}
	for _, cred := range g.gitCreds {
		if !strings.HasPrefix(rawURL, "https://"+cred.RepoPrefix) {
			continue
		}
		token, err := g.secrets.Get(ctx, secrets.Scope{Env: cred.InfisicalEnv, Path: cred.InfisicalPath}, cred.InfisicalKey)
		if err != nil {
			return nil, fmt.Errorf("git credential for %q: %w", cred.RepoPrefix, err)
		}
		auth := base64.StdEncoding.EncodeToString([]byte("oauth2:" + token))
		key := "http.https://" + cred.RepoPrefix + ".extraHeader"
		return []string{
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=" + key,
			"GIT_CONFIG_VALUE_0=Authorization: Basic " + auth,
		}, nil
	}
	return nil, nil
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
	env, err := g.credEnv(ctx, g.repoURL)
	if err != nil {
		return "", fmt.Errorf("git credential: %w", err)
	}
	if _, err := os.Stat(filepath.Join(g.dir, ".git")); errors.Is(err, os.ErrNotExist) {
		if err := g.gitEnv(ctx, "", env, "clone", "--branch", g.branch, "--single-branch", g.repoURL, g.dir); err != nil {
			return "", fmt.Errorf("clone: %w", err)
		}
	} else {
		if err := g.gitEnv(ctx, g.dir, env, "fetch", "origin", g.branch); err != nil {
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

// loadHead parses the IaC config from the working tree as-is (no sync) and
// returns it with the checked-out HEAD SHA. Used after checkoutRef has already
// placed an arbitrary ref in g.dir.
func (g *GitSyncer) loadHead(ctx context.Context) (*config.Repo, string, error) {
	repo, err := config.Load(g.dir)
	if err != nil {
		return nil, "", fmt.Errorf("load config: %w", err)
	}
	sha, err := g.gitOut(ctx, g.dir, "rev-parse", "HEAD")
	if err != nil {
		return nil, "", fmt.Errorf("rev-parse: %w", err)
	}
	return repo, strings.TrimSpace(sha), nil
}

// checkoutRef fetches an arbitrary git ref (branch, tag, refs/pull/N/head, or
// commit SHA) into an isolated temp checkout and returns a sibling syncer bound
// to it. Read-only state (store, secrets, credentials, paths) is shared with
// the parent; only the working dir and branch differ — so planning or checking
// a ref never disturbs the orchestrator's live working tree (which the drift
// reconciler and deploy path render from). The caller must invoke cleanup.
func (g *GitSyncer) checkoutRef(ctx context.Context, ref string) (sib *GitSyncer, cleanup func(), err error) {
	tmp, err := os.MkdirTemp("", "shuttle-ref-")
	if err != nil {
		return nil, func() {}, fmt.Errorf("temp dir: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }
	fail := func(format string, a ...any) (*GitSyncer, func(), error) {
		cleanup()
		return nil, func() {}, fmt.Errorf(format, a...)
	}

	env, err := g.credEnv(ctx, g.repoURL)
	if err != nil {
		return fail("git credential: %w", err)
	}
	if err := g.git(ctx, "", "init", "-q", tmp); err != nil {
		return fail("init: %w", err)
	}
	if err := g.git(ctx, tmp, "remote", "add", "origin", g.repoURL); err != nil {
		return fail("remote add: %w", err)
	}
	if err := g.gitEnv(ctx, tmp, env, "fetch", "--depth", "1", "origin", ref); err != nil {
		return fail("fetch %s: %w", ref, err)
	}
	if err := g.git(ctx, tmp, "checkout", "-q", "FETCH_HEAD"); err != nil {
		return fail("checkout %s: %w", ref, err)
	}

	clone := *g
	clone.dir = tmp
	clone.branch = ref
	return &clone, cleanup, nil
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
	g.dispatchHostCaddyConfigs(repo)
	current, err := g.store.CurrentSHAs(ctx)
	if err != nil {
		return nil, fmt.Errorf("current state: %w", err)
	}
	plan := ComputePlan(repo, CurrentState(current), sha)
	dispatched := g.dispatchPlan(ctx, repo, plan.Steps, toSet(onlyServices))
	g.reconcileRemovals(ctx, repo)
	return dispatched, nil
}

// reconcileRemovals tears down services that have left the repo. It records the
// repo's services as present, flips any previously-present service that is now
// absent to removed, and dispatches a container teardown for each removed
// service whose containers have not yet been brought down (idempotent, so an
// offline agent is retried next tick). Volumes are always kept here; their
// deletion is governed separately by each service's delete_volumes policy.
func (g *GitSyncer) reconcileRemovals(ctx context.Context, repo *config.Repo) {
	repoNames := make(map[string]bool, len(repo.Services))
	for _, svc := range repo.Services {
		repoNames[svc.Name] = true
		if err := g.store.MarkServicePresent(ctx, svc.Name, svc.Host, svc.DeleteVolumes); err != nil {
			slog.Error("mark service present failed", "service", svc.Name, "err", err)
		}
	}

	present, err := g.store.PresentServices(ctx)
	if err != nil {
		slog.Error("list present services failed", "err", err)
		return
	}
	for _, svc := range present {
		if repoNames[svc] {
			continue
		}
		// The just-removed service is no longer in the repo; its last-known
		// policy was captured in the lifecycle row, so read it back.
		pol, err := g.store.ServiceDeleteVolumes(ctx, svc)
		if err != nil {
			slog.Error("read delete_volumes policy failed", "service", svc, "err", err)
			pol = config.DeleteVolumesManual
		}
		if err := g.store.MarkServiceRemoved(ctx, svc, purgeAfterForPolicy(pol, time.Now())); err != nil {
			slog.Error("mark service removed failed", "service", svc, "err", err)
		}
	}

	awaiting, err := g.store.ServicesAwaitingTeardown(ctx)
	if err != nil {
		slog.Error("list services awaiting teardown failed", "err", err)
		return
	}
	for _, sl := range awaiting {
		if err := g.dispatchTeardown(sl.Service, sl.Host, false); err != nil {
			slog.Warn("teardown dispatch failed (will retry)", "service", sl.Service, "host", sl.Host, "err", err)
			continue
		}
		if err := g.store.MarkContainersRemoved(ctx, sl.Service); err != nil {
			slog.Error("mark containers removed failed", "service", sl.Service, "err", err)
			continue
		}
		slog.Info("service teardown dispatched", "service", sl.Service, "host", sl.Host)
		g.bus.Publish(Event{
			Type: EventServiceRemoved, Service: sl.Service, Host: sl.Host,
			Message: "containers torn down (volumes kept)",
		})
	}
}

// dispatchTeardown sends a teardown command to the host's agent. removeVolumes
// is false for ordinary removals (data kept) and true for volume purges.
func (g *GitSyncer) dispatchTeardown(service, host string, removeVolumes bool) error {
	cmd := &shuttlev1.OrchestratorCommand{
		Payload: &shuttlev1.OrchestratorCommand_Teardown{
			Teardown: &shuttlev1.TeardownRequest{
				DeployId:      newID(),
				Service:       service,
				RemoveVolumes: removeVolumes,
			},
		},
	}
	return g.registry.Send(host, cmd)
}

// purgeAfterForPolicy maps a delete_volumes policy to a volume-deletion deadline
// (epoch ms) relative to now: "immediate" => now, a duration => now+duration,
// "manual" (or anything unparseable) => nil (no scheduled purge; wait for prune).
func purgeAfterForPolicy(policy string, now time.Time) *int64 {
	switch policy {
	case config.DeleteVolumesImmediate:
		ms := now.UnixMilli()
		return &ms
	case config.DeleteVolumesManual, "":
		return nil
	}
	d, err := config.ParseHumanDuration(policy)
	if err != nil {
		return nil // unknown policy: keep volumes until pruned
	}
	ms := now.Add(d).UnixMilli()
	return &ms
}

// PurgeExpiredVolumes deletes the volumes of removed services whose scheduled
// deadline has passed (immediate or duration policies). Manual-policy services
// are left for an explicit prune. Returns the services whose purge was
// dispatched. Driven by the drift reconciler tick.
func (g *GitSyncer) PurgeExpiredVolumes(ctx context.Context) ([]string, error) {
	due, err := g.store.ServicesAwaitingPurge(ctx, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	return g.dispatchPurges(ctx, due), nil
}

// PruneVolumes force-deletes the volumes of every removed service that still has
// them, regardless of policy or deadline. Backs the manual prune command.
func (g *GitSyncer) PruneVolumes(ctx context.Context) ([]string, error) {
	pending, err := g.store.ServicesPendingVolumes(ctx)
	if err != nil {
		return nil, err
	}
	return g.dispatchPurges(ctx, pending), nil
}

// dispatchPurges sends a volume-removing teardown for each service and marks it
// purged on success. An offline host's purge is skipped and retried later.
func (g *GitSyncer) dispatchPurges(ctx context.Context, services []ledger.ServiceLifecycle) []string {
	var purged []string
	for _, sl := range services {
		if err := g.dispatchTeardown(sl.Service, sl.Host, true); err != nil {
			slog.Warn("volume purge dispatch failed (will retry)", "service", sl.Service, "host", sl.Host, "err", err)
			continue
		}
		if err := g.store.MarkVolumesPurged(ctx, sl.Service); err != nil {
			slog.Error("mark volumes purged failed", "service", sl.Service, "err", err)
			continue
		}
		slog.Info("service volumes purged", "service", sl.Service, "host", sl.Host)
		g.bus.Publish(Event{
			Type: EventVolumesPurged, Service: sl.Service, Host: sl.Host,
			Message: "named volumes deleted",
		})
		purged = append(purged, sl.Service)
	}
	return purged
}

// applyRoutes pushes the repo's desired routes to Caddy when configured.
func (g *GitSyncer) applyRoutes(ctx context.Context, repo *config.Repo) {
	if g.caddy == nil {
		return
	}
	routes, err := RoutesFromRepo(repo)
	if err != nil {
		slog.Error("derive caddy routes failed", "err", err)
		return
	}
	if err := g.caddy.ApplyRoutes(ctx, routes, g.httpsRedirect); err != nil {
		slog.Error("apply caddy routes failed", "err", err)
		return
	}
	slog.Info("caddy routes applied", "count", len(routes))
}

// dispatchHostCaddyConfigs pushes each host its Caddy ingress config, so an
// agent running a Caddy sidecar (--caddy) can apply it via CaddyConfigRequest.
// Hosts with no routable services, or whose agent is not connected, are skipped.
func (g *GitSyncer) dispatchHostCaddyConfigs(repo *config.Repo) {
	for _, h := range repo.Hosts {
		cfgJSON, ok, err := HostCaddyConfigJSON(repo, h.Name, g.httpsRedirect)
		if err != nil {
			slog.Error("build caddy config failed", "host", h.Name, "err", err)
			continue
		}
		if !ok {
			continue
		}
		cmd := &shuttlev1.OrchestratorCommand{
			Payload: &shuttlev1.OrchestratorCommand_CaddyConfig{
				CaddyConfig: &shuttlev1.CaddyConfigRequest{ConfigJson: string(cfgJSON)},
			},
		}
		if err := g.registry.Send(h.Name, cmd); err != nil {
			slog.Debug("skip caddy config push (host not connected)", "host", h.Name, "err", err)
			continue
		}
		slog.Info("caddy config pushed", "host", h.Name)
	}
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

// Hosts syncs the IaC repo and returns its declared hosts. Used by the
// enrollment endpoints to validate the host an agent enrolls as.
func (g *GitSyncer) Hosts(ctx context.Context) ([]config.Host, error) {
	repo, _, err := g.syncAndLoad(ctx)
	if err != nil {
		return nil, err
	}
	return repo.Hosts, nil
}

// DeployAtSHA checks out the repo at sha, renders the named service's compose +
// env at that revision, and dispatches a deploy. Used by the manual deploy and
// rollback HTTP endpoints, which must ship real compose (unlike a bare
// DeployRequest). Returns the deploy ID and the resolved host.
//
// The working copy is left detached at sha; the next Reconcile resets it to the
// branch tip.
func (g *GitSyncer) DeployAtSHA(ctx context.Context, service, sha string, triggeredBy ledger.TriggeredBy) (deployID, host string, err error) {
	// Ensure the repo (and its history) is present. Credentials are injected per
	// invocation via credEnv (never persisted in the remote URL), so an existing
	// clone's fetch must carry them too.
	if _, statErr := os.Stat(filepath.Join(g.dir, ".git")); errors.Is(statErr, os.ErrNotExist) {
		if _, syncErr := g.Sync(ctx); syncErr != nil {
			return "", "", syncErr
		}
	} else {
		env, credErr := g.credEnv(ctx, g.repoURL)
		if credErr != nil {
			return "", "", fmt.Errorf("git credential: %w", credErr)
		}
		if fetchErr := g.gitEnv(ctx, g.dir, env, "fetch", "origin", g.branch); fetchErr != nil {
			return "", "", fmt.Errorf("fetch: %w", fetchErr)
		}
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
				DeployId:     deployID,
				Service:      step.Service,
				Sha:          step.SHA,
				Env:          env,
				ComposeYaml:  composeYAML,
				UpdatePolicy: svc.UpdatePolicy,
			},
		},
	}
	if err := g.registry.Send(step.Host, cmd); err != nil {
		_ = g.store.MarkStatus(ctx, deployID, ledger.StatusFailed)
		g.bus.Publish(Event{
			Type: EventDeployFailed, Service: step.Service, Host: step.Host,
			DeployID: deployID, SHA: step.SHA, Status: string(ledger.StatusFailed),
			Message: "send to agent failed",
		})
		return "", fmt.Errorf("send to agent: %w", err)
	}
	slog.Info("deploy dispatched", "deploy_id", deployID, "service", step.Service, "host", step.Host, "sha", step.SHA)
	queuedType := EventDeployQueued
	if triggeredBy == ledger.TriggeredByRollback {
		queuedType = EventRollbackQueued
	}
	g.bus.Publish(Event{
		Type: queuedType, Service: step.Service, Host: step.Host,
		DeployID: deployID, SHA: step.SHA, Status: string(ledger.StatusPending),
		Detail: map[string]string{"triggered_by": string(triggeredBy)},
	})
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
	env, err := g.credEnv(ctx, rp.Repo)
	if err != nil {
		return nil, fmt.Errorf("git credential: %w", err)
	}
	cacheDir := filepath.Join(g.dir+".remotes", sanitizeRepoKey(rp.Repo))
	if _, err := os.Stat(filepath.Join(cacheDir, ".git")); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(cacheDir), 0o755); err != nil {
			return nil, fmt.Errorf("mkdir remote cache: %w", err)
		}
		if err := g.gitEnv(ctx, "", env, "clone", "--branch", branch, "--single-branch", "--depth", "1", rp.Repo, cacheDir); err != nil {
			return nil, fmt.Errorf("clone remote %s: %w", rp.Repo, err)
		}
	} else {
		if err := g.gitEnv(ctx, cacheDir, env, "fetch", "--depth", "1", "origin", branch); err != nil {
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

// renderEnv resolves the service's secrets. The service's env_from selects the
// Infisical environment; the secrets are merged from a shared base folder and
// the service's own folder (secret_path / secrets_path_template), with the
// service folder winning on conflicts. When EnvSchema is set only those keys are
// included, otherwise the whole merged set is passed through.
func (g *GitSyncer) renderEnv(ctx context.Context, svc config.Service) (map[string]string, error) {
	if g.secrets == nil {
		return nil, nil
	}
	basePath, svcPath := config.ResolveSecretsPaths(g.secretsBasePath, g.secretsPathTemplate, svc.SecretPath, svc.Name)

	all, err := g.secrets.GetAll(ctx, secrets.Scope{Env: svc.EnvFrom, Path: basePath})
	if err != nil {
		return nil, fmt.Errorf("secrets (base %q): %w", basePath, err)
	}
	if svcPath != basePath {
		specific, err := g.secrets.GetAll(ctx, secrets.Scope{Env: svc.EnvFrom, Path: svcPath})
		if err != nil {
			return nil, fmt.Errorf("secrets (service %q): %w", svcPath, err)
		}
		maps.Copy(all, specific) // service-specific keys override the shared base
	}

	if len(svc.EnvSchema) == 0 {
		return all, nil
	}
	env := make(map[string]string, len(svc.EnvSchema))
	var missing []string
	for _, key := range svc.EnvSchema {
		if v, ok := all[key]; ok {
			env[key] = v
		} else {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("service %q: env keys declared in env_schema but missing from secrets (base %q, service %q): %s",
			svc.Name, basePath, svcPath, strings.Join(missing, ", "))
	}
	return env, nil
}

func (g *GitSyncer) git(ctx context.Context, dir string, args ...string) error {
	return g.gitEnv(ctx, dir, nil, args...)
}

// gitEnv runs git with extraEnv appended to the process environment. Used to
// inject credential config (see credEnv) without writing it to disk or args.
func (g *GitSyncer) gitEnv(ctx context.Context, dir string, extraEnv []string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
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
