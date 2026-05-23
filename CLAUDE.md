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
make build      # version-stamped binary
make test       # go test -race ./internal/...   (unit; this is the default gate)
make lint       # golangci-lint run ./...         (v2 config — see CI notes below)
make proto      # buf generate -> gen/            (run after editing proto/)
make certs      # dev mTLS material under ./certs (gitignored)
```

Always run `make test` before committing. The repo is kept race-clean.

## Package map

| Path | Responsibility |
|------|----------------|
| `cmd/shuttle/` | Cobra CLI: `main.go` (root), `orchestrator.go`, `agent.go`, `enroll.go`, `prune.go`, `version.go`. Wiring only — no business logic. |
| `proto/shuttle/v1/` | gRPC contracts (`deploy.proto`, `agent.proto`). Source of truth for the transport. |
| `gen/shuttle/v1/` | Generated Go (committed). Regenerate with `make proto`; never hand-edit. |
| `internal/config/` | Strict YAML loaders. `LoadOrchestratorConfig` (the orchestrator's `config.yml`) and `Load` (the IaC repo). |
| `internal/ledger/` | SQLite append-only deploy store (`RecordDeploy`, `MarkStatus`, `RollbackTarget`, `CurrentSHAs`, `SeenNonce`) + the `service_lifecycle` table (`MarkServicePresent`, `MarkServiceRemoved`, `ServicesAwaitingTeardown`) tracking which services are still in the repo. |
| `internal/secrets/` | `Provider` interface + `Fake` (tests) + `InfisicalProvider`. `NewProvider(name)` factory. |
| `internal/webhook/` | Webhook payload parse, HMAC `X-Hub-Signature-256` verify, nonce replay guard. |
| `internal/infisical/` | Infisical secret-change webhook: payload decode + `x-infisical-signature` HMAC verify (`t=<ts>,v1=<hex>` over `<ts>.<body>`). |
| `internal/mtls/` | gRPC TLS 1.3 creds: `ServerCreds`/`ClientCreds` (mutual) + `ServerTLSCreds`/`ClientTLSCreds` (server-auth only, for token auth). |
| `internal/token/` | Agent enrollment token mint (256-bit) + SHA-256 hash. |
| `internal/orchestrator/` | The brain. See below. |
| `internal/agent/` | Agent run loop (`client.go`) + the Compose `Driver` (`compose.go`). |

### `internal/orchestrator/` internals

| File | Responsibility |
|------|----------------|
| `server.go` | gRPC `AgentServiceServer`: the bidi `Register` stream, deploy-result → ledger. |
| `auth.go` | `TokenStreamInterceptor` — validates the agent's bearer token, pins the stream to its host. |
| `enroll.go` | `GET /hosts` + `POST /enroll`: mint host-scoped tokens, build the agent command. |
| `registry.go` | Connected-agent registry; heartbeat tracking + eviction; `Send(host, cmd)`. |
| `git.go` | `GitSyncer`: clone/pull (git shell-out), render compose+env, dispatch deploys. |
| `diff.go` | `ComputePlan` — desired (repo) vs actual (ledger SHAs) → deploy steps. |
| `reconcile.go` | `StateTracker` + `DriftReconciler` (periodic SHA + container drift heal). |
| `caddy.go` | Caddy Admin API client; `RoutesFromRepo` + `caddy_snippet` injection. |
| `http.go` | HTTP control plane (`/deploy`, `/rollback`, `/deploys`, `/healthz`, `/webhook`, `/webhook/infisical`, `/hosts`, `/enroll`, `/prune`). |
| `secretdeps.go` | `ServicesUsingSecret` — maps a changed Infisical (env, folder) to the services that read it (used by the Infisical webhook for selective redeploy). |
| `debounce.go` | `changeDebouncer` — coalesces a burst of Infisical changes into one reconcile pass. |

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
changed (env, path) → `changeDebouncer` coalesces a burst → `Reconcile` of just
the affected services. Folder matching is exact (non-recursive), mirroring
`renderEnv`'s per-folder reads.

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
  `secrets_path_template` with `{service}`, else the base). `renderEnv` reads
  both folders in that environment and merges them (service folder wins), then
  filters by `env_schema`, producing the `.env` shipped with the compose file.
  Folder paths must be absolute. The provider stays generic `(env, path) →
  secrets`; all path *policy* lives in the orchestrator. A key declared in
  `env_schema` but absent from the resolved secrets is a **hard error** (not a
  warning) — the deploy fails loudly rather than shipping a silently-empty `.env`.
- **CLI loads `CWD/.env` at startup.** `main` calls `config.LoadDotEnv(".env")`
  before any subcommand, so the `INFISICAL_*` provider vars (and others) can come
  from a local `.env`. The real environment always wins; a missing file is not an
  error. Tiny built-in parser (no `godotenv` dep), consistent with the project's
  shell-out-over-library bias.
- **Webhook auth = HMAC `X-Hub-Signature-256` + nonce replay guard (10 min TTL).**
  Matches the GitHub webhook convention; the nonce guard blocks replays.
- **Infisical webhook = HMAC `x-infisical-signature` → selective, debounced
  redeploy.** `infisical_webhook_secret` enables `POST /webhook/infisical`. The
  signature is `t=<ts>,v1=<hmac>` over `<ts>.<body>` (Stripe/Infisical style;
  skipped only if no secret is configured). A change carries an (env, folder);
  `ServicesUsingSecret` maps it to exactly the services reading that folder
  (non-recursive match, since `renderEnv` reads folders non-recursively) and only
  those are reconciled — no full redeploy. A burst of edits is coalesced over
  `infisical_webhook_debounce` (default 5s) so N rapid changes trigger one pass.
- **HTTP auth = static bearer token (v1).** Simple to start; OIDC is planned.
- **Agent auth = mTLS *or* enrollment token.** Either present a client cert
  (mutual TLS) or a host-scoped bearer token over server-auth TLS. The token path
  (`shuttle enroll` → `POST /enroll`) avoids per-agent cert distribution: only the
  orchestrator needs a cert. Tokens are long-lived, revocable, stored as SHA-256
  hashes, and validated by `TokenStreamInterceptor`, which pins the stream to the
  token's host so a token can't register a different one. Token over a non-TLS
  transport works but logs a cleartext warning.
- **Compose `Driver` is an interface, parameterized by binary + subcommand.**
  The default targets `docker compose`; the `synology` preset points at
  `/usr/local/bin/docker` for DSM Container Manager. New targets are new presets,
  selected by the agent's `--driver` flag.
- **`buf` for proto tooling**, with `buf lint` and `buf breaking` gating `main`.

### Explicitly dropped / not done

- **ECS target — dropped.** It doesn't fit the agent-runs-compose model (it's
  orchestrator-side, needs `aws-sdk-go-v2`, and can't be verified locally). The
  AWS SDK still appears as an *indirect* dep via Infisical, not for ECS.
- **OIDC HTTP auth — planned**, not yet built (bearer token for now).

## CI notes (non-obvious)

- `.golangci.yml` is **v2 format** → needs golangci-lint v2. CI installs it with
  `install-mode: goinstall` so it builds against the runner's Go (1.25);
  prebuilt binaries lag and reject the `go 1.25.x` module target. Local:
  `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`.
- `go.mod`'s `go 1.25.x` cannot be lowered — a transitive dep requires it
  (`go mod tidy` re-bumps).
- `release.yml` fires on `v*` tags → GoReleaser publishes archives + checksums +
  multi-arch `ghcr.io/neikow/shuttle` images. `goreleaser check` validates config.
- GHA actions currently run on Node.js 20 (deprecated; forced to Node 24 on
  2026-06-02). Bump action versions before then.

## Conventions

- Match surrounding style; the codebase favors small files, table-driven tests,
  and explicit error wrapping (`fmt.Errorf("…: %w", err)`).
- Touching `proto/` means re-running `make proto` and committing `gen/`.
- Don't commit `certs/` (gitignored) or real secrets.
