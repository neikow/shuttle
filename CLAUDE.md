# CLAUDE.md

Guidance for Claude Code (and humans) working in this repository. It explains
the architecture, the package map, and *why* each major decision was made — read
it before changing structural code.

## What this is

Shuttle is a self-hosted, git-driven IaC deployment platform shipped as a single
Go binary with two subcommands: `shuttle orchestrator` and `shuttle agent`. The
orchestrator watches an IaC git repo and dispatches Docker Compose deploys to
agents on managed hosts, recording every deploy in an append-only SQLite ledger
that powers rollback and drift detection.

## Build / test / lint

```sh
make build      # version-stamped binary (no embedded UI)
make build-ui   # binary WITH the embedded web UI (runs `make web` first, -tags embedui)
make web        # build the React UI into web/dist (npm ci + vite build)
make web-dev    # Vite dev server, proxies API to a local orchestrator on :8080
make web-test   # frontend unit/component tests (Vitest + RTL)
make dev-up     # full dev cluster: orchestrator + UI + 2 Docker-in-Docker hosts (dev-down/dev-logs)
make test       # go test -race ./internal/...   (unit; this is the default gate)
make test-integration # go test -race -tags integration ./test/integration/... (real Docker; opt-in)
make lint       # golangci-lint run ./...         (v2 config — see CI notes below)
make proto      # buf generate -> gen/            (run after editing proto/)
make certs      # dev mTLS material under ./certs (gitignored)
```

Always run `make test` before committing. The repo is kept race-clean.

## Package map

| Path | Responsibility |
|------|----------------|
| `cmd/shuttle/` | Cobra CLI: `main.go` (root), `orchestrator.go`, `agent.go`, `enroll.go`, `prune.go`, `check.go`, `version.go`, `webhook.go`, `events.go` (SSE event stream client), `init.go` (interactive bootstrap wizard), `join.go` (`shuttle agent join`: redeem a join token over a pinned HTTPS client, persist creds, start the agent), `backup.go` (`shuttle backup`/`restore`: snapshot + restore the ledger from local files), `audit.go` (`shuttle audit`: read the control-plane audit log from a running orchestrator), `token.go` (`shuttle token create`/`list`/`revoke`: manage named, role-scoped control-plane tokens). Wiring only — no business logic. |
| `proto/shuttle/v1/` | gRPC contracts (`deploy.proto`, `agent.proto`). Source of truth for the transport. |
| `gen/shuttle/v1/` | Generated Go (committed). Regenerate with `make proto`; never hand-edit. |
| `internal/config/` | Strict YAML loaders. `LoadOrchestratorConfig` (the orchestrator's `config.yml`), `Load` (the IaC repo), and `LoadRepoOrchestratorConfig` (`orchestrator.yaml` in the IaC repo — optional repo-managed overrides). |
| `internal/ledger/` | SQLite append-only deploy store (`RecordDeploy`, `MarkStatus`, `RollbackTarget`, `CurrentSHAs`, `SeenNonce`) + the `service_lifecycle` table (`MarkServicePresent`, `MarkServiceRemoved`, `ServicesAwaitingTeardown`) tracking which services are still in the repo + the `repo_webhooks` table (`CreateRepoWebhook`, `LookupRepoWebhook`, `ListRepoWebhooks`, `DeleteRepoWebhook`) for service-specific deploy webhooks + the `join_tokens` table (`CreateJoinToken`, `RedeemJoinToken`, `PurgeExpiredJoinTokens`) for single-use SSH-like agent enrollment + `backup.go` (`BackupTo` via SQLite `VACUUM INTO`, `Verify`, `RestoreInto`) for `shuttle backup`/`restore` + the `audit_log` table (`RecordAudit`, `ListAudit`) — append-only actor+action record of every control-plane mutation + the `control_tokens` table (`CreateControlToken`, `LookupControlToken`, `ListControlTokens`, `RevokeControlToken`) — named, role-scoped HTTP bearer tokens (hashed at rest) backing RBAC + the `deploy_logs` table (`RecordDeployLogs`, `DeployLogs`) — captured agent output per deploy, keyed by deploy_id, append-only. |
| `internal/secrets/` | `Provider` interface + `Fake` (tests) + `InfisicalProvider` + `FileProvider` (dotenv files under `SHUTTLE_SECRETS_DIR`, no external dep). `NewProvider(name)` factory (`infisical`/`file`/`none`). |
| `internal/webhook/` | Webhook payload parse, HMAC `X-Hub-Signature-256` verify, nonce replay guard. |
| `internal/infisical/` | Infisical secret-change webhook: payload decode + `x-infisical-signature` HMAC verify (`t=<ts>,v1=<hex>` over `<ts>.<body>`). |
| `internal/mtls/` | gRPC TLS 1.3 creds: `ServerCreds`/`ClientCreds` (mutual) + `ServerTLSCreds`/`ClientTLSCreds` (server-auth only, for token auth). `pin.go`: `SPKIPin` + `PinnedHTTPClient` for trust-on-first-use cert pinning during `agent join`. |
| `internal/token/` | Agent enrollment + join token mint (256-bit) + SHA-256 hash. |
| `internal/orchestrator/` | The brain. See below. |
| `internal/agent/` | Agent run loop (`client.go`) + the Compose `Driver` (`compose.go`) + zero-downtime rolling strategy (`rolling.go`) + Caddy sidecar manager (`caddy.go`). |
| `web/` | React + Vite + TS read-only dashboard (Tailwind v4 + Radix). `embed.go`/`embed_stub.go` gate embedding the built `dist/` behind the `embedui` build tag. Consumes the existing control-plane endpoints. |
| `test/integration/` | End-to-end tests (`//go:build integration`) that drive the real `shuttle` binary against a live Docker daemon: build → orchestrator + agent → `POST /deploy` → container serves → ledger records success. Excluded from the default unit gate; run via `make test-integration`. An untagged `doc.go` keeps `go build ./...`/lint happy. |

### `internal/orchestrator/` internals

| File | Responsibility |
|------|----------------|
| `server.go` | gRPC `AgentServiceServer`: the bidi `Register` stream, deploy-result → ledger. Records the agent's reported build version on register and logs a warning on agent/orchestrator version skew (`SetVersion`). |
| `auth.go` | `TokenStreamInterceptor` — validates the agent's bearer token, pins the stream to its host. |
| `rbac.go` | HTTP RBAC: `Role` (read<deploy<admin) + `ParseRole`/`roleRank`, `resolveRole` (static bearer→admin, else ledger `control_tokens` lookup, else OIDC JWT via `looksLikeJWT`+`SetOIDC`), and the `requireRole(min, handler)` middleware (401 unauth / 403 insufficient) that replaces the old flat `bearerAuth`. Stashes the resolved `identity{Name,Role}` in the request context so the audit log records the token name / OIDC subject as actor. |
| `oidc.go` | `OIDCAuthenticator`: per-user OpenID Connect auth. `NewOIDCAuthenticator` does discovery (`github.com/coreos/go-oidc`) against the issuer at boot; `verify` checks JWT signature/JWKS + issuer + `audience`, maps the `roles_claim` through `role_mapping`→`Role` (highest wins), identity = `username_claim`. The third source in `resolveRole`. |
| `control_tokens_http.go` | `POST /tokens` (mint, returns plaintext once), `GET /tokens` (list, no hashes), `DELETE /tokens/{id}` (revoke) — all admin-only; create/revoke audited (`token.create`/`token.revoke`). |
| `enroll.go` | `GET /hosts` + `POST /enroll` (mint a single-use join token) + `POST /enroll/redeem` (join-token-authed: exchange for a host-scoped agent token, hand back gRPC addr/SAN/CA). |
| `registry.go` | Connected-agent registry; heartbeat tracking + eviction; per-agent build version; `Send(host, cmd)`; `Snapshot` (host, last-seen, version). |
| `git.go` | `GitSyncer`: clone/pull (git shell-out), render compose+env, dispatch deploys. |
| `diff.go` | `ComputePlan` — desired (repo) vs actual (ledger SHAs) → deploy steps. |
| `reconcile.go` | `StateTracker` + `DriftReconciler` (periodic SHA + container drift heal). |
| `caddy.go` | Caddy Admin API client; `RoutesFromRepo` + `caddy_snippet` injection. |
| `http.go` | HTTP control plane (`/whoami`, `/deploy`, `/rollback`, `/deploys`, `/deploys/{id}/logs`, `/audit`, `/tokens`, `/healthz`, `/readyz`, `/overview`, `/webhook`, `/webhook/infisical`, `/webhook/repo/{id}`, `/webhooks/repo`, `/hosts`, `/enroll`, `/enroll/redeem`, `/prune`, `/plan`, `/check`, `/events`, `/metrics`). Each authed route is wrapped in `requireRole` (see `rbac.go`) at its minimum tier; `ServeHTTP` sets baseline security headers (+ CSP on `/ui`). `GET /whoami` (read tier) echoes the caller's resolved `{name, role}` so the UI can gate which mutation screens it shows. `EnableMetrics(h, requireAuth)` optionally gates `/metrics` at the read tier. `EnableRepoWebhooks` registers the service-specific webhook CRUD + trigger endpoints. |
| `audit.go` | Audit-log recording helpers: `recordAudit` (best-effort, nil-safe), `auditActor` (X-Actor header → actor, else `operator`), `clientIP` (RemoteAddr, never trusts XFF), and the action/result constants. Mutation handlers in `http.go`/`enroll.go` call `recordAudit` on success and failure. |
| `overview.go` | `GET /overview` — single-screen snapshot merging connected-agent liveness (`Registry.Snapshot`, incl. each agent's reported `agent_version`) with the latest reported container state per service (`StateTracker.Snapshot`). A host shows if connected *or* it has any reported service, so an offline host with known services still appears (`Connected=false`). Backs the UI Overview tab. |
| `plan.go` | `GitSyncer.Plan` — read-only desired-vs-actual diff: sync repo, diff every service against `ledger.CurrentSHAs` → per-service `create`/`update`/`unchanged`/`remove`. Dispatches nothing. Backs `GET /plan` and `shuttle plan`. `PlanRef(ref)` diffs an arbitrary ref (branch/tag/`refs/pull/N/head`/SHA) via an isolated temp checkout (`checkoutRef`), so CI can preview a PR branch without touching the live working tree (`?ref=` / `--ref`). |
| `metrics.go` | `Metrics` — subscribes to the `EventBus` and exposes Prometheus metrics at `GET /metrics` (`shuttle_events_total{type}`, `shuttle_deploy_duration_seconds`, `shuttle_connected_agents`, `shuttle_event_bus_dropped_total`). |
| `notify.go` | `Notifier` — subscribes to the `EventBus` and POSTs matching events to outbound webhooks (Slack `{"text"}`, Discord `{"content"}`, or generic `webhook` = raw event JSON). Per-target `events` filter (empty = all). Best-effort: bounded-concurrent, time-limited sends; failures logged not retried; never blocks the deploy path. Configured by `notifications:` in `config.yml`. |
| `ratelimit.go` | `ipRateLimiter` — per-client-IP token bucket (`golang.org/x/time/rate`) wrapping the unauthenticated endpoints (webhooks + `/enroll/redeem`); 429 + `Retry-After` over the limit. Buckets idle out; keyed on `RemoteAddr` (not spoofable `X-Forwarded-For`). Tunable via `webhook_rate_limit_per_minute`. |
| `secretdeps.go` | `ServicesUsingSecret` — maps a changed Infisical (env, folder) to the services that read it (used by the Infisical webhook for selective redeploy). |
| `debounce.go` | `changeDebouncer` — coalesces a burst of Infisical changes into one reconcile pass. |
| `secretpoll.go` | `SecretPoller` — periodic fingerprint poll of the Infisical folders services read; redeploys on change. Fallback for undelivered webhooks. Stores only SHA-256 fingerprints, never secret values. |
| `check.go` | `GitSyncer.Check` — read-only validation pass: sync+load the repo and verify every service's `env_schema` keys resolve in the provider. Collects all problems (no fail-fast), dispatches nothing. Backs `GET /check` and `shuttle check` (remote mode hits the running orchestrator so CI needs no local config). `CheckRef(ref)` validates an arbitrary ref via the same isolated `checkoutRef` as plan (`?ref=` / `--ref`). |
| `ui.go` | `EnableUI` — serves the embedded `web/dist` SPA under `/ui/` (no-op unless built `-tags embedui`). Static bundle is unauthenticated; the browser app authenticates its own API calls with the pasted bearer token, so control-plane endpoints stay `bearerAuth`-protected. SPA fallback to `index.html` for client routes. |
| `events.go` | `EventBus` — in-process pub/sub for orchestrator events (`deploy.queued/succeeded/failed/rolled_back`, `rollback.queued`, `drift.detected`, `service.removed`, `volumes.purged`). Publishers: `dispatch`, the deploy-result handler, the drift reconciler, teardown/purge. Bounded per-subscriber buffers (drop on overflow) + a replay ring. Consumed by the SSE stream (`/events`), metrics (`metrics.go`), and outbound notifications (`notify.go`). |

## Request flows

**Webhook deploy:** `POST /webhook` → HMAC verify + replay guard → async
`GitSyncer.Reconcile` → `Sync` (git pull) → `config.Load` → `ComputePlan` vs
`ledger.CurrentSHAs` → for each changed service: render compose + env, record a
pending ledger row, `registry.Send` a `DeployRequest` → agent runs
`docker compose up` → streams `DeployResponse` back → ledger `MarkStatus`. Caddy
routes are re-pushed each reconcile.

**Infisical webhook deploy:** `POST /webhook/infisical` → `infisical.Handler`
verifies the HMAC signature → `ServicesUsingSecret` syncs the repo and finds the
services whose resolved secret folders (base or service) exactly match the
changed (env, path) → `changeDebouncer` coalesces a burst → `ForceDeploy` of just
the affected services. Folder matching is exact (non-recursive), mirroring
`renderEnv`'s per-folder reads. `ForceDeploy` (not the SHA-gated `Reconcile`)
because a secret change does not move the repo SHA, so the diff would be empty
and nothing would re-render.

**Infisical secret polling:** when `infisical_poll_interval` is set, a
`SecretPoller` (`secretpoll.go`) ticks on that interval as a fallback for when
webhooks aren't delivered. Each tick loads the working copy (no git op — the
drift reconciler keeps it synced), fingerprints every distinct (env, folder) the
repo's services read (SHA-256 over the sorted key/value set; **values are never
stored**), and `ForceDeploy`s the services whose fingerprint changed. The first
pass only seeds fingerprints (no redeploy), so a restart doesn't storm.

**Zero-downtime deploy (rolling):** the default for every service
(`update_policy: rolling`). The agent's `rollingApply` (`rolling.go`): `pull` →
`compose up -d --no-deps --no-recreate --scale S=2N` (new containers start
alongside the old) → join the *new* containers to the Caddy network (via the
`OnNewContainers` hook, so Caddy round-robins to both) → wait until the new ones
are healthy (Docker healthcheck → `healthy`; none → `running` after a grace) →
`docker rm -f` the old → settle the replica count. Any failure before the old
containers are removed aborts, removes the new containers, and leaves the old
version serving (deploy reported FAILED). Requires the project to run two-up: no
fixed published host port, no `container_name` — `shuttle check` warns otherwise.
`update_policy: recreate` opts back into compose's stop-then-start. Rollback
always uses recreate.

**Manual deploy / rollback:** `POST /deploy/{service}` and
`POST /rollback?service=…&steps=N` use `GitSyncer.DeployAtSHA` (checkout the
target SHA, render real compose+env, dispatch). Rollback resolves the target SHA
via `ledger.RollbackTarget`.

**Drift heal:** agents report `ContainerState` every ~30s. `DriftReconciler`
ticks every 60s: SHA drift → `Reconcile`; crashed/missing containers →
`ForceDeploy`. The agent's deployed-set is in-memory, so on restart it
reconciles from reality — `seedFromDisk` re-tracks every `<work_dir>/<service>`
compose workspace, so the report/heal loop resumes for services deployed before
the restart (recorded SHA is unknown post-restart and left empty; container
drift keys on status, not SHA).

**Service removal:** every `Reconcile` marks the repo's services present in
`service_lifecycle`; a service that was present but is now absent from the repo
flips to removed. For each removed service whose containers aren't yet down,
`reconcileRemovals` sends a `TeardownRequest` → agent runs `docker compose down`
against the persisted workspace and stops tracking it. Teardown is idempotent
and retried until `registry.Send` succeeds (so an offline host heals when it
reconnects). Volumes are **kept** here — their deletion is governed separately
by each service's `delete_volumes` policy (see below).

**Volume deletion:** a service's `delete_volumes` policy (captured in
`service_lifecycle` while present, so it survives removal) decides when its named
volumes go. At removal `purgeAfterForPolicy` sets a deadline: `immediate` → now,
a duration (`"7 days"`) → now+duration, `manual` (default) → none. Each
`DriftReconciler` tick calls `PurgeExpiredVolumes` → for services past their
deadline, sends a `TeardownRequest{remove_volumes:true}` → agent runs
`docker compose down --volumes` and deletes the workspace. `manual` services
wait for an explicit prune: `POST /prune` (or `shuttle prune`) →
`PruneVolumes` force-purges every removed service whose volumes remain. Purges
are marked on `registry.Send` success and retried for offline hosts.

## Design decisions & rationale

These are deliberate. Don't reverse them without updating this file.

- **Single Go binary, two subcommands.** One artifact to ship and version; the
  orchestrator/agent split is a runtime flag, not a separate build.
- **Two-tier config split: `config.yml` (bootstrap) vs `orchestrator.yaml` (repo-managed).** Settings needed to *start* the orchestrator (bearer token, repo URL, webhook secret, TLS) can't live in git — the orchestrator can't clone the repo without them. Everything that changes at runtime without a restart (Caddy, secrets paths, git credentials) lives in `orchestrator.yaml` in the IaC repo, reloaded atomically on each reconcile via `atomic.Pointer[RepoOrchestratorConfig]`. A parse error keeps old values and never blocks deploys — a broken commit is recoverable with a revert push. `shuttle init` scaffolds both files.
- **`shuttle init` as the blessed bootstrap path.** Auto-generates bearer token and webhook secret (32-byte `crypto/rand`, hex-encoded); writes `config.yml` and `.env` at mode 0600; scaffolds the IaC repo idempotently (second run never overwrites user-edited files); optionally wires GitHub Actions. Separating the wizard (`promptInitOptions`) from the applier (`applyInit`) keeps the logic fully unit-testable without stdin.
- **gRPC bidi stream, agent dials out.** Agents open the connection to the
  orchestrator, so managed hosts need *no* inbound firewall holes. Commands flow
  down the same stream that heartbeats and state flow up.
- **Orchestrator renders, agents are dumb.** All git, diffing, secret
  resolution, and templating happen on the orchestrator. Agents receive a
  finished compose file + env and just run it. Keeps agents trivial and
  secret-free at rest.
- **SQLite via `modernc.org/sqlite` (pure Go, no CGO), WAL mode.** Single-file
  ledger, static binary, no external DB to operate. The ledger is *append-only*:
  rollback is "redeploy an older recorded SHA," not "mutate state."
- **Backup/restore via SQLite `VACUUM INTO`, not file copy.** A single-file
  ledger is one `rm` from total deploy-history loss, so `shuttle backup` is
  first-class. It uses `VACUUM INTO` (not `cp shuttle.db`) because the live DB
  runs in WAL mode — a raw copy can capture a torn, mid-checkpoint state. `VACUUM
  INTO` takes a read transaction and emits a consistent, plain (non-WAL) snapshot
  *while the orchestrator keeps running*. `shuttle restore` is the offline
  inverse: it `Verify`s the file is a real ledger (queries `deploys`), then
  atomically installs it (temp file + rename) and removes stale `-wal`/`-shm`
  sidecars so the snapshot is authoritative. Both operate on local files only —
  no running orchestrator needed — so they fit cron/backup tooling.
- **Service lifecycle is mutable state, separate from the append-only ledger.**
  The `deploys` table can't express "no longer desired," and a removed service's
  config (e.g. its `delete_volumes` policy) is gone from the repo. So a small
  mutable `service_lifecycle` table records, per service: present/removed, the
  removal/teardown timestamps, and the last-known volume policy — captured while
  the service is still in the repo so it survives the removal. Teardown is
  idempotent (re-`docker compose down` is harmless), so the orchestrator marks
  progress on `registry.Send` success and retries offline hosts next tick.
- **`delete_volumes` defaults to `manual` (data is never auto-deleted).** Removing
  a service from the repo tears down its containers but, by default, keeps its
  volumes until an explicit `prune` — so an accidental repo deletion loses no
  data. Opt into deletion per service: `true`/`immediate`, or a duration
  (`"7 days"`) that defers the purge. The duration parser (`ParseHumanDuration`)
  extends `time.ParseDuration` with day/week and spelled-out units.
- **git via shell-out, not a Go git library.** Mirrors the agent's
  `docker compose` shell-out and avoids a heavy `go-git` dependency. The git CLI
  is already a hard runtime requirement.
- **`git_credentials` — per-repo HTTPS tokens from Infisical, injected at call time.** Private IaC repos and remote compose pointers need authentication without storing credentials on disk. Each `git_credentials` entry specifies a `repo_prefix` and the Infisical key to fetch; `GitSyncer.credEnv` injects the token at each git operation as a one-shot `http.https://<repo_prefix>.extraHeader` via `GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_0`/`GIT_CONFIG_VALUE_0` environment variables — so it is **never written to `.git/config` on disk and never appears in the process argument list**. The header value is `Authorization: Basic base64("oauth2:<token>")` (HTTP Basic with an `oauth2` username, the GitHub/GitLab token convention), scoped to the credential's `repo_prefix` so it is not sent to any other remote a single command might contact. The token is fetched fresh every call (no caching) so rotation in Infisical takes effect immediately. `CheckGitCredentials` validates all entries during `shuttle check`.
- **Caddy for ingress (Admin API at `:2019`).** Per-host Caddy instance with
  automatic Let's Encrypt. Routes are derived from service `domains` + `port`
  and pushed as a full config each reconcile (declarative, no drift).
  `caddy_snippet` lets a service inject extra handlers ahead of the proxy.
  `https_redirect` (orchestrator config) controls the server's `listen`: false →
  `[:80, :443]` (plaintext served on :80, no redirect — claiming :80 suppresses
  Caddy's auto-redirect); true → `[:443]` only, so Caddy's automatic HTTPS stands
  up its own :80 server that 308-redirects to HTTPS and answers ACME HTTP-01.
- **Secrets via a `Provider` interface.** Infisical is the first real provider;
  `Fake` backs tests. `Get`/`GetAll` take a `Scope{Env, Path}`: a service's
  `env_from` is the environment (empty → `INFISICAL_ENV`), and the folder comes
  from `config.ResolveSecretsPaths` — a shared `secrets_base_path` (default
  `/shared`) merged with the service's own folder (`secret_path`, else
  `secrets_path_template` with `{service}`, which itself defaults to
  `/services/{service}` when unset). `renderEnv` reads
  both folders in that environment and merges them (service folder wins), then
  filters by `env_schema`, producing the `.env` shipped with the compose file.
  Folder paths must be absolute. The provider stays generic `(env, path) →
  secrets`; all path *policy* lives in the orchestrator. A key declared in
  `env_schema` but absent from the resolved secrets is a **hard error** (not a
  warning) — the deploy fails loudly rather than shipping a silently-empty `.env`.
  A second provider, **`file`** (`FileProvider`), needs no external service:
  `secrets_provider: file` maps the same `Scope{Env, Path}` to a dotenv file at
  `<SHUTTLE_SECRETS_DIR>/<env>/<path>.env` (default env `SHUTTLE_SECRETS_ENV`,
  else `production`). Because path policy lives in the orchestrator, the file
  layout mirrors the Infisical folders exactly, so switching providers needs no
  repo changes. A missing file is an empty set (env_schema is still the one place
  a missing key fails); the `Path` is cleaned absolute before joining so a `..`
  can't escape the root. Secrets can thus live as a tmpfs mount, projected k8s
  secrets, or sops-decrypted files instead of in Infisical.
- **CLI loads `CWD/.env` at startup.** `main` calls `config.LoadDotEnv(".env")`
  before any subcommand, so the `INFISICAL_*` provider vars (and others) can come
  from a local `.env`. The real environment always wins; a missing file is not an
  error. Tiny built-in parser (no `godotenv` dep), consistent with the project's
  shell-out-over-library bias.
- **Webhook auth = HMAC `X-Hub-Signature-256` + nonce replay guard (10 min TTL).**
  Matches the GitHub webhook convention; the nonce guard blocks replays.
- **Unauthenticated endpoints are IP rate-limited.** `/webhook`,
  `/webhook/infisical`, `/webhook/repo/{id}`, and `/enroll/redeem` take no
  bearer, so a per-client-IP token bucket (`ratelimit.go`) bounds DoS/abuse of
  handlers that do real work (HMAC verify, reconcile, a ledger write for redeem)
  before any auth gate, and slows guessing of the 256-bit repo-webhook IDs.
  Default 120/min/IP (`webhook_rate_limit_per_minute`; negative disables). Keyed
  on `RemoteAddr`, **not** `X-Forwarded-For` — trusting XFF would let an attacker
  forge the header to evade the limit; a trusted-proxy/XFF mode can come later.
  The limiter sits only on the unauthenticated endpoints; bearer-authed routes
  rely on the token. `/enroll/redeem` is *additionally* protected by the
  single-use, short-TTL join token it carries; the rate limit just bounds abuse
  of the handler itself.
- **Baseline security headers on every response; CSP on the UI.** `ServeHTTP`
  sets `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, and
  `Referrer-Policy: no-referrer` on all responses (cheap defense-in-depth that
  also covers the unauthenticated `/ui/` bundle and probe endpoints), and a
  restrictive `Content-Security-Policy` (`uiCSP`) on `/ui` paths only —
  same-origin scripts/connects, `'unsafe-inline'` styles (component libraries set
  style attributes at runtime), `frame-ancestors 'none'`. CSP is scoped to the UI
  because only it serves rendered HTML; JSON/metrics responses don't need it.
- **`/metrics` auth is opt-in.** `/metrics` is unauthenticated by default
  (standard Prometheus scrape model; labels are low-cardinality so no topology
  leaks). `metrics_require_auth: true` gates it at the **read** tier via
  `requireRole`, for deployments that expose `/metrics` on an untrusted network
  and want it behind a token.
- **Infisical webhook = HMAC `x-infisical-signature` → selective, debounced
  redeploy.** `infisical_webhook_secret` enables `POST /webhook/infisical`. The
  signature is `t=<ts>,v1=<hmac>` over `<ts>.<body>` (Stripe/Infisical style;
  skipped only if no secret is configured). A change carries an (env, folder);
  `ServicesUsingSecret` maps it to exactly the services reading that folder
  (non-recursive match, since `renderEnv` reads folders non-recursively) and only
  those are reconciled — no full redeploy. A burst of edits is coalesced over
  `infisical_webhook_debounce` (default 5s) so N rapid changes trigger one pass.
- **Infisical polling = fingerprint-diff fallback, values never stored.** When
  webhooks aren't delivered, `infisical_poll_interval` enables `SecretPoller`: it
  periodically hashes each (env, folder) the repo reads and redeploys the
  services whose hash changed. Only SHA-256 fingerprints are held in memory — the
  orchestrator never persists secret plaintext. A secret change doesn't move the
  repo SHA, so both the webhook and poller paths use `ForceDeploy`, not the
  SHA-gated `Reconcile`.
- **HTTP auth = static bearer token + RBAC role tokens.** The single static
  `bearer_token` from `config.yml` remains the **bootstrap admin** (backward
  compatible: existing configs, CI, and the UI keep full access). On top of it,
  **named, role-scoped control tokens** (`control_tokens` ledger table, SHA-256
  hashed at rest) add least-privilege credentials. Three totally-ordered roles —
  `read` < `deploy` < `admin`: *read* = list/inspect (`/deploys`,
  `/deploys/{id}/logs`, `/audit`,
  `/overview`, `/plan`, `/check`, `/events`, `/hosts`); *deploy* = + `/deploy`,
  `/rollback`, `/prune`; *admin* = + `/enroll`, `/webhooks/repo` CRUD, `/tokens`
  CRUD. Each authed route is wrapped in `requireRole(min, …)` (`rbac.go`):
  `resolveRole` tries three identity sources in order — the static bearer
  (→admin, constant-time), then a named ledger control token, then (when
  configured) an **OIDC JWT** — and a missing/invalid token → 401, a valid token
  with too low a role → 403. The resolved identity (token **name**, or the OIDC
  subject) is stashed in the request context and becomes the **audit actor**, so
  RBAC and the audit log reinforce each other — see the audit-log decision.
  Tokens are minted via the admin-only `POST /tokens` (plaintext returned once,
  like `shuttle enroll`), listed without their hash, and revoked by ID; managed
  by `shuttle token create/list/revoke`.
- **OIDC HTTP auth = per-user identity layered on the same role model.** When
  `oidc:` is configured (`issuer` + `audience`), `resolveRole` accepts an
  OpenID Connect **JWT** as a third identity source (after the static bearer and
  named control tokens). `internal/orchestrator/oidc.go` `OIDCAuthenticator`
  delegates JWT signature/JWKS verification to `github.com/coreos/go-oidc` (the
  canonical Go verifier — crypto correctness is not hand-rolled, cf. cosign /
  prometheus histograms), validating the issuer + `audience` (`aud`) and mapping
  a configurable claim (`roles_claim`, default `groups`) through `role_mapping`
  to a `Role` — highest-ranked matched role wins. The caller's identity (audit
  actor) is the `username_claim` (default `sub`). A validly-signed token that
  maps to **no** role is authenticated but unauthorized → **403, not 401**
  (mirrors a too-low control token); `resolveRole`'s `ok` now means "the caller
  was authenticated", not "has a usable role". Only JWT-shaped tokens
  (`looksLikeJWT`: three non-empty dot segments) incur a signature verify, so an
  opaque static/control token never does. OIDC is **additive**: the static
  bearer stays the break-glass admin and control tokens are untouched. Discovery
  is a startup network call (`NewOIDCAuthenticator`), so a typo'd role or an
  unreachable issuer fails the orchestrator at boot rather than silently denying
  every user. This realizes the per-user identity the RBAC/audit work was built
  toward; the role model and `requireRole` enforcement are reused unchanged.
- **Audit log = append-only actor+action record, separate from the deploys
  ledger.** Every control-plane *mutation* (`deploy`, `rollback`, `prune`,
  `enroll`, `enroll.redeem`, `webhook.create`, `webhook.delete`) writes an
  `audit_log` row capturing actor, action, target, source IP, result
  (success/failure), and a short detail string — so an operator can answer "who
  deployed this / who minted that agent token". It is distinct from the `deploys`
  table (which records deploy *state*, not actor identity) and never mutated
  after insert. The actor is resolved in precedence order: a **named RBAC control
  token** contributes its name (real identity — see the HTTP-auth decision);
  otherwise, since the static bootstrap bearer has no name, a caller may
  self-identify via an optional `X-Actor` header (CI sets it to the triggering
  user/workflow); absent both, the actor is the generic `operator`. The redeem
  path has no bearer, so its actor is `agent`.
  Source IP is taken from `RemoteAddr`, **never** `X-Forwarded-For` (spoofable).
  Recording is **best-effort**: a failed audit write is logged but never fails the
  action — the audit log must not gate the control plane. Exposed read-only at
  bearer-authed `GET /audit` (`?action=` filter, `?limit=` 1–200) and consumed by
  `shuttle audit`. RBAC token names and OIDC subjects now supply real per-user
  actor identity; the `X-Actor`/`operator` fallback remains only for the static
  bootstrap bearer.
- **Per-deploy logs are persisted, not just streamed.** The agent already sends
  the full captured output of a deploy/rollback in the terminal `DeployResponse`
  (`repeated LogLine`); the orchestrator now stores it in the `deploy_logs`
  ledger table keyed by `deploy_id` (`server.go` deploy-result handler →
  `RecordDeployLogs`) and serves it read-only at `GET /deploys/{id}/logs` (read
  tier). This lets an operator answer *why* a deploy failed from the control
  plane / UI instead of SSHing to the host to grep agent logs. Writing logs is
  **best-effort** — a failed log write is logged but never changes the deploy
  result (the ledger status is the source of truth). Logs aren't streamed live
  (the agent batches them into one final message), so the endpoint is a
  point-in-time read, surfaced in the UI's Deploys tab behind a per-row **Logs**
  button. Deploys recorded before this feature (or that failed before the agent
  ran) simply have none.
- **Liveness vs readiness: `/healthz` always-200, `/readyz` gated.** `/healthz`
  answers 200 for the life of the process (liveness). `/readyz` is backed by an
  `atomic.Bool` the orchestrator flips true once its listeners are up and **false
  at the first shutdown signal**, *before* draining — so a load balancer routes
  new traffic away while in-flight requests finish against the already-graceful
  `GracefulStop` + timed `http.Shutdown`. Both probes are unauthenticated so a LB
  can poll them tokenless.
- **Agent auth = mTLS *or* enrollment token.** Either present a client cert
  (mutual TLS) or a host-scoped bearer token over server-auth TLS. The token path
  avoids per-agent cert distribution: only the
  orchestrator needs a cert. Tokens are long-lived, revocable, stored as SHA-256
  hashes, and validated by `TokenStreamInterceptor`, which pins the stream to the
  token's host so a token can't register a different one. Token over a non-TLS
  transport works but logs a cleartext warning.
- **Agent version is reported and surfaced for skew visibility.** The agent
  sends its build version in the `RegisterRequest` (`agent_version`, already in
  the proto). The orchestrator stores it on the registry connection, exposes it
  per host in `GET /overview` (`agent_version`), and — when its own version is
  known (`AgentServiceServer.SetVersion`, wired from the binary's `Version`) —
  logs a warning when a connecting agent's version differs. Detection only, not
  enforcement: a deploy is never refused on skew (mismatched versions still
  interoperate over the stable proto), but operators can *see* which hosts lag a
  rollout. Empty versions (an un-stamped `dev` build) are treated as unknown and
  never trigger a skew warning.
- **SSH-like enrollment = single-use join token + cert-pin TOFU.** The token is
  minted and consumed in two steps so the operator's powerful control-plane
  bearer never reaches the target host. `shuttle enroll` (bearer-authed
  `POST /enroll`) mints a **short-lived, single-use join token** bound to the host
  (`ledger.join_tokens`, hashed at rest, default 15 min TTL) and prints a single
  `shuttle agent join` one-liner. The enroll client computes the orchestrator's
  certificate **pin** (`mtls.SPKIPin` — base64 SHA-256 of the SubjectPublicKeyInfo)
  from the live TLS peer cert over its already-authenticated channel and embeds it
  as `--pin`. On the host, `shuttle agent join` redeems the join token at the
  unauthenticated `POST /enroll/redeem` over a **pin-verified** HTTPS client
  (`mtls.PinnedHTTPClient`, trust-on-first-use — no CA file to copy); the
  orchestrator atomically claims the token (`RedeemJoinToken`: single UPDATE
  guarded on `used_at IS NULL AND expires_at > now`), mints the real host-scoped
  agent token, and hands back the gRPC address, SAN, and **CA PEM**. `join`
  persists the token + CA under `--work-dir` at mode 0600 and starts the agent; a
  later plain `shuttle agent` auto-loads them, so restarts need no secret on the
  command line. Redeem failures (unknown / expired / already-used) return an
  undifferentiated 401. The legacy direct `shuttle agent --token` path is
  unchanged for mTLS and manual setups. `shuttle enroll` resolves its URL +
  bearer token by precedence (`resolveEnrollCreds`): explicit `--url`/`--token`
  flags > `--config` (the orchestrator's `config.yml`, reading
  `advertise_control_url` + `bearer_token`) > `SHUTTLE_URL`/`SHUTTLE_TOKEN` env
  (a local `.env` works, since `main` loads it). `advertise_control_url` must be
  the externally reachable URL — it is both the endpoint enroll calls (and pins)
  and the `redeem-url` baked into the join command — so it can't reuse
  `http_addr`. On the orchestrator host, `shuttle enroll --config config.yml
  --host web-1` then needs no secret on the command line.
- **Zero-downtime is the default, via compose scale not orchestrator magic.**
  Rolling lives entirely in the agent (`rolling.go`): it leans on the existing
  sidecar-Caddy model where Caddy dials the `<service>` network alias, so two
  containers sharing that alias are load-balanced by Docker's DNS — bring up the
  new, health-gate, cull the old. The orchestrator only passes `update_policy`
  on the `DeployRequest`. The safety invariant: nothing old is removed until the
  new is healthy, so a failed deploy never causes downtime. The hard constraint
  (can't run two-up with a fixed host port or `container_name`) is surfaced as a
  `shuttle check` warning rather than enforced, because the runtime abort already
  fails safe. `recreate` remains available per service.
- **Compose `Driver` is an interface, parameterized by binary + subcommand.**
  The default targets `docker compose`; the `synology` preset points at
  `/usr/local/bin/docker` for DSM Container Manager. New targets are new presets,
  selected by the agent's `--driver` flag.
- **In-process event bus, best-effort delivery.** Orchestrator state changes
  (deploy queued/succeeded/failed, rollback, drift, teardown, volume purge) are
  published to a single `EventBus` (`events.go`) that notifications and metrics
  subscribe to — one event model instead of re-instrumenting scattered `slog`
  sites per feature. Delivery is best-effort: each subscriber has a bounded
  buffer and a slow consumer has events *dropped* (counted via `Dropped()`),
  never blocking the deploy path. The bus is ephemeral (a small replay ring for
  late subscribers); the **ledger remains the source of truth** for deploy
  history. All methods are nil-safe, so the bus is an optional dependency every
  publisher holds unconditionally.
- **`plan` is read-only and dual-mode.** `shuttle plan` previews what a
  reconcile would do (`create`/`update`/`unchanged`/`remove` per service)
  without dispatching. Remote mode (`GET /plan`, bearer) asks the running
  orchestrator so the diff is against the live ledger; local mode (`--config`)
  clones the repo and diffs against the ledger at `--data-dir` — with no ledger
  (CI) every service is `create`. The diff core (`buildPlanReport`) is pure
  (repo + current SHAs → report), so it reuses the same `ledger.CurrentSHAs`
  the reconcile path uses, keeping plan and apply consistent. `--exit-code`
  exits 2 on a non-empty plan for CI gating.
- **Prometheus metrics off the event bus, unauthed `/metrics`.** `Metrics`
  (`metrics.go`) subscribes to the `EventBus` and turns events into Prometheus
  metrics. `prometheus/client_golang` (despite the usual minimal-dep bias)
  because correct histograms + exposition aren't worth hand-rolling. Labels are
  deliberately **low-cardinality — event type only, never service or host
  names** — so `/metrics` can be scraped unauthenticated (standard scrape model)
  without leaking topology; `metrics_require_auth: true` gates it at the read
  tier for untrusted networks (see the `/metrics`-auth decision). Connected-agent
  gauge and dropped-event counter read
  live from the registry/bus at scrape time (`GaugeFunc`/`CounterFunc`); deploy
  duration is a histogram, timed by matching a terminal event to its queued
  event by deploy ID. Uses its own registry, not the global default.
- **Notifications via SSE, not WebSocket.** `GET /events` streams `EventBus`
  events as Server-Sent Events (`data: <json>` frames; JSON carries the type so
  a client filters on one stream). SSE over WebSocket because the feed is
  one-way (server→client), works over plain HTTP with no extra dependency
  (stdlib `http.Flusher`), and reconnects natively. On connect the handler
  replays the bus backlog, then forwards live events; a periodic comment line
  (`: keep-alive`) stops idle proxies closing the connection. Bearer-authed
  (events leak service names + SHAs). A slow reader blocks only its own stream —
  the bus drops its events rather than stalling the deploy path. `shuttle
  events` is the CLI consumer.
- **Outbound notifications = the same bus, pushed to chat/webhooks.** Where
  `/events` is pull (a client connects), `notify.go`'s `Notifier` is push: it
  subscribes to the `EventBus` and POSTs each matching event to configured sinks
  — `slack` (`{"text"}`), `discord` (`{"content"}`), or generic `webhook` (the
  raw `Event` JSON). Targets live in `config.yml` under `notifications:` (a
  Slack/Discord webhook URL is a secret, so **not** the repo-managed
  `orchestrator.yaml`); each target's optional `events:` list filters by type
  (empty = all). Delivery is deliberately best-effort and decoupled from the
  deploy path: sends are bounded-concurrent (a small semaphore) and time-limited
  (10s client timeout), a saturated target backpressures only the bus (which
  drops *its* subscriber's events, never the publisher's), and failures are
  logged, not retried — the ledger + `/events` remain the durable record. The
  bus's nil-safety lets the notifier be wholly optional (`NewNotifier` returns
  nil when no targets are configured, and `Run` is a no-op on nil).
- **Service-specific deploy webhooks — 256-bit ID as the secret, no HMAC.** `POST /webhooks/repo` creates a webhook scoped to one service, returning a random 256-bit ID stored in the ledger (`repo_webhooks` table). `POST /webhook/repo/{id}` triggers a `ForceDeploy` of the bound service with no additional auth — the ID entropy is sufficient. This is the simplest integration point for external systems (container registries, third-party CI) that need to trigger a single-service redeploy without exposing the orchestrator bearer token. Managed via `EnableRepoWebhooks` (called only when git sync is configured).
- **Web UI: embedded React SPA, build-tag-gated, with role-gated mutations.**
  `web/` is a Vite + TS + Tailwind-v4 + Radix dashboard (sharp aesthetic — radius
  forced near 0). It consumes the *existing* control-plane endpoints — no new read
  backend — and now drives the existing RBAC'd **mutation** endpoints behind
  role-gated screens. Shipped inside the single binary via `//go:embed` behind the
  `embedui` build tag (`web/embed.go` vs `embed_stub.go`), so a plain `go build
  ./...` needs no `web/dist` and the default Go test/lint gate is unaffected;
  `make build-ui` and goreleaser build with the tag after `make web`. `ui.go`
  serves the bundle **unauthenticated** under `/ui/` (SPA fallback to
  `index.html`) — the API stays `requireRole`-protected and the browser app
  authenticates its own calls with a token the user pastes (kept in
  `localStorage`). SSE auth needs a header, which `EventSource` can't set, so the
  events view uses `@microsoft/fetch-event-source`.
  **Mutations are gated client-side by the caller's role** (`GET /whoami` →
  `{name, role}`; `web/src/role.ts` mirrors the Go `read<deploy<admin` order):
  operational actions — redeploy, rollback (Deploys/Plan), prune (Overview) — show
  at the **deploy** tier; token CRUD, repo-webhook CRUD, and agent enrollment
  (Tokens/Webhooks tabs + Hosts) show at the **admin** tier. This gating is
  convenience only — `requireRole` is the real gate, so a forged role just earns a
  401/403 on the request. None of the exposed mutations edit desired service
  config, so the drift reconciler never fights the UI; **git write-back config
  editing is deliberately out of scope** (a separate, larger feature). The
  enrollment screen can't compute the server SPKI pin in-browser, so it surfaces
  the join token + expiry and defers the fully-pinned one-liner to `shuttle
  enroll`/`shuttle agent join`. Frontend has its own test gate (Vitest + React
  Testing Library, `make web-test`), run in a dedicated CI `web` job; the role
  matrix is also asserted end-to-end in `test/integration/ui_mutations_test.go`.
- **`buf` for proto tooling**, with `buf lint` and `buf breaking` gating `main`.

### Explicitly dropped / not done

- **ECS target — dropped.** It doesn't fit the agent-runs-compose model (it's
  orchestrator-side, needs `aws-sdk-go-v2`, and can't be verified locally). The
  AWS SDK still appears as an *indirect* dep via Infisical, not for ECS.
- **OIDC HTTP auth — done.** Per-user OpenID Connect JWTs are a third identity
  source on the control plane, mapped to the read/deploy/admin role model (see
  the OIDC HTTP-auth decision). The static bearer remains the break-glass admin.

## CI notes (non-obvious)

- `.golangci.yml` is **v2 format** → needs golangci-lint v2. CI installs it with
  `install-mode: goinstall` so it builds against the runner's Go (1.25);
  prebuilt binaries lag and reject the `go 1.25.x` module target. Local:
  `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`.
- `go.mod`'s `go 1.25.x` cannot be lowered — a transitive dep requires it
  (`go mod tidy` re-bumps).
- `integration.yml` runs the Docker-backed E2E suite (`make test-integration`)
  on PRs + `main` + manual dispatch. Kept separate from `push.yml` (the fast
  unit/lint/vet/vulncheck gate) because it is slower and pulls images. The
  suite skips itself when Docker isn't available, so it's a no-op where it
  can't run rather than a hard failure.
- `release.yml` fires on `v*` tags → GoReleaser publishes archives + checksums +
  multi-arch `ghcr.io/neikow/shuttle` images. `goreleaser check` validates config.
  Supply chain: the release also emits a **per-archive SBOM** (syft, `sboms:`)
  and **keyless cosign signatures** — `signs:` over `checksums.txt` (transitively
  covers every archive) and `docker_signs:` over the images + manifests. Keyless
  (Fulcio/Rekor) means no signing key to manage: the workflow grants
  `id-token: write` and the job's GitHub OIDC identity is the signer, so
  `release.yml` installs `cosign` + `syft` before the GoReleaser step. Verify a
  release with `cosign verify` / `cosign verify-blob`, pinning
  `--certificate-identity-regexp 'https://github.com/neikow/shuttle/.*'` and
  `--certificate-oidc-issuer https://token.actions.githubusercontent.com`
  (recipes in `.goreleaser.yaml` comments).
- GHA actions currently run on Node.js 20 (deprecated; forced to Node 24 on
  2026-06-02). Bump action versions before then.

## Conventions

- Match surrounding style; the codebase favors small files, table-driven tests,
  and explicit error wrapping (`fmt.Errorf("…: %w", err)`).
- Touching `proto/` means re-running `make proto` and committing `gen/`.
- Don't commit `certs/` (gitignored) or real secrets.
