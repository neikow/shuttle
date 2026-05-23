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
| `cmd/shuttle/` | Cobra CLI: `main.go` (root), `orchestrator.go`, `agent.go`, `version.go`. Wiring only — no business logic. |
| `proto/shuttle/v1/` | gRPC contracts (`deploy.proto`, `agent.proto`). Source of truth for the transport. |
| `gen/shuttle/v1/` | Generated Go (committed). Regenerate with `make proto`; never hand-edit. |
| `internal/config/` | Strict YAML loaders. `LoadOrchestratorConfig` (the orchestrator's `config.yml`) and `Load` (the IaC repo). |
| `internal/ledger/` | SQLite append-only deploy store (`RecordDeploy`, `MarkStatus`, `RollbackTarget`, `CurrentSHAs`, `SeenNonce`). |
| `internal/secrets/` | `Provider` interface + `Fake` (tests) + `InfisicalProvider`. `NewProvider(name)` factory. |
| `internal/webhook/` | Webhook payload parse, HMAC `X-Hub-Signature-256` verify, nonce replay guard. |
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
| `http.go` | HTTP control plane (`/deploy`, `/rollback`, `/deploys`, `/healthz`, `/webhook`, `/hosts`, `/enroll`). |

## Request flows

**Webhook deploy:** `POST /webhook` → HMAC verify + replay guard → async
`GitSyncer.Reconcile` → `Sync` (git pull) → `config.Load` → `ComputePlan` vs
`ledger.CurrentSHAs` → for each changed service: render compose + env, record a
pending ledger row, `registry.Send` a `DeployRequest` → agent runs
`docker compose up` → streams `DeployResponse` back → ledger `MarkStatus`. Caddy
routes are re-pushed each reconcile.

**Manual deploy / rollback:** `POST /deploy/{service}` and
`POST /rollback?service=…&steps=N` use `GitSyncer.DeployAtSHA` (checkout the
target SHA, render real compose+env, dispatch). Rollback resolves the target SHA
via `ledger.RollbackTarget`.

**Drift heal:** agents report `ContainerState` every ~30s. `DriftReconciler`
ticks every 60s: SHA drift → `Reconcile`; crashed/missing containers →
`ForceDeploy`.

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
- **git via shell-out, not a Go git library.** Mirrors the agent's
  `docker compose` shell-out and avoids a heavy `go-git` dependency. The git CLI
  is already a hard runtime requirement.
- **Caddy for ingress (Admin API at `:2019`).** Per-host Caddy instance with
  automatic Let's Encrypt. Routes are derived from service `domains` +
  healthcheck port and pushed as a full config each reconcile (declarative, no
  drift). `caddy_snippet` lets a service inject extra handlers ahead of the proxy.
- **Secrets via a `Provider` interface.** Infisical is the first real provider;
  `Fake` backs tests. The orchestrator filters secrets by each service's
  `env_schema` so an agent only ever receives the keys it declares.
- **Webhook auth = HMAC `X-Hub-Signature-256` + nonce replay guard (10 min TTL).**
  Matches the GitHub webhook convention; the nonce guard blocks replays.
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
