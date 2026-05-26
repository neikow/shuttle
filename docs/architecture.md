# Architecture

Shuttle is one Go binary with two roles. The **orchestrator** is a stateful
control plane; **agents** are stateless executors on each managed host.

## Components

```
                         ┌───────────────────────────────────────────┐
                         │              orchestrator                  │
   IaC git repo          │                                            │
   ┌──────────┐  webhook │   GitSyncer ── ComputePlan ── dispatch     │
   │ hosts &  │ ───────► │      │  (diff repo vs ledger)    │         │
   │ services │  (HMAC)  │   git pull                       │ gRPC    │
   └──────────┘          │      │                           ▼ (mTLS)  │
                         │   SQLite ledger ◄── deploy results         │
                         │      │                           │         │
                         │   Caddy client                   │         │
                         └──────┼───────────────────────────┼─────────┘
                                ▼                            ▼
                          Caddy Admin API            ┌──────────────┐
                          (TLS ingress)              │   agent(s)   │
                                                     │ docker compose│ ─► containers
                                                     └──────────────┘
```

### Orchestrator

Owns all decision-making and durable state:

1. **Git sync** (`GitSyncer`, `internal/orchestrator/git.go`) — clones/pulls the
   IaC repo (git CLI shell-out), parses it with `config.Load`.
2. **Diff** (`ComputePlan`, `diff.go`) — compares desired state (repo) against
   actual state (the SHA last successfully deployed per service, from the
   ledger) and produces deploy steps.
3. **Render & dispatch** — for each step, renders the service's compose file
   (local or fetched from a remote pointer) and its env (secrets filtered by the
   service `env_schema`), records a pending ledger row, and sends a
   `DeployRequest` to the host's agent.
4. **Ledger** (`internal/ledger`) — append-only SQLite store of every deploy;
   the source of truth for "what is running" and "what to roll back to."
5. **Ingress** (`caddy.go`) — derives routes from service `domains` + `port`
   and pushes a full config to Caddy's Admin API on each reconcile.
6. **Drift reconciler** (`reconcile.go`) — periodically heals both SHA drift
   (repo changed) and container drift (a container crashed/disappeared).

### Agent

A thin executor (`internal/agent`):

- Dials *out* to the orchestrator and opens the bidirectional `Register` stream.
- Receives `DeployRequest`/`RollbackRequest` commands, writes the compose file +
  `.env` to a per-service work dir, and runs the Compose `Driver`
  (`docker compose up -d`).
- Streams logs and a final `DeployResponse` back.
- Sends heartbeats (~30s) and `ContainerState` so the orchestrator can detect
  drift.
- Manages a **Caddy sidecar** container on a shared Docker network; the
  orchestrator pushes route config via `CaddyConfigRequest` on each reconcile.

The agent holds no durable state and no secrets at rest — everything it needs
arrives on the stream per deploy.

## The transport: one bidirectional stream

`AgentService.Register` is a single gRPC bidi stream (`proto/shuttle/v1/agent.proto`):

- **Up (agent → orchestrator):** `AgentEvent` = register | heartbeat |
  deploy_result | container_state.
- **Down (orchestrator → agent):** `OrchestratorCommand` = deploy | rollback |
  caddy_config | teardown.

Because the **agent initiates** the connection, managed hosts expose no inbound
ports. The orchestrator's `Registry` (`registry.go`) tracks live streams by host
name and routes commands with `Send(host, cmd)`, evicting agents that stop
heartbeating.

## State model

The ledger is **append-only**. A deploy is a row: `(deploy_id, service, host,
sha, status, triggered_by, started_at)`. "Current state" is a derived view —
`CurrentSHAs` returns the latest successful SHA per service. Rollback is not a
state mutation; it is *redeploying an older recorded SHA*
(`RollbackTarget` → `DeployAtSHA`). This makes history immutable and rollback
auditable.

The append-only `deploys` table can't express "no longer desired," so a small
mutable `service_lifecycle` table tracks whether each service is still in the
repo. When a service disappears from the repo, `reconcileRemovals` flips it to
removed and dispatches a `teardown` (the agent runs `docker compose down`
against the persisted workspace). Named volumes are kept until an explicit
volume-deletion policy says otherwise.

## Deploy triggers

| Trigger | Path |
|---------|------|
| Git push | `POST /webhook` (HMAC) → async `Reconcile` |
| Manual | `POST /deploy/{service}` → `DeployAtSHA` at HEAD |
| Rollback | `POST /rollback` → `RollbackTarget` → `DeployAtSHA` |
| Drift | `DriftReconciler` tick → `Reconcile` / `ForceDeploy` |
| Removal | service gone from repo → `reconcileRemovals` → `teardown` (keeps volumes) |
| Infisical webhook | `POST /webhook/infisical` (HMAC) → `ServicesUsingSecret` → debounced `ForceDeploy` |
| Infisical poll | `SecretPoller` tick → fingerprint diff → `ForceDeploy` changed services |
| Service webhook | `POST /webhook/repo/{id}` (ID = secret) → `ForceDeploy` bound service |

## Event bus

`EventBus` (`internal/orchestrator/events.go`) is an in-process pub/sub for
orchestrator state changes. Publishers include the deploy path, the drift
reconciler, teardown, and volume purge. Subscribers include metrics and the SSE
event stream.

Delivery is best-effort: each subscriber has a bounded buffer and slow consumers
have events dropped (counted via `Dropped()`) rather than blocking the deploy
path. A small replay ring lets late subscribers catch up on connect. All methods
are nil-safe.

Event types: `deploy.queued`, `deploy.succeeded`, `deploy.failed`,
`deploy.rolled_back`, `rollback.queued`, `drift.detected`, `service.removed`,
`volumes.purged`.

## Observability

### Prometheus metrics (`GET /metrics`)

`Metrics` (`metrics.go`) subscribes to the `EventBus` and exposes metrics via
`prometheus/client_golang`. The endpoint is unauthed (standard scrape model).
Labels are deliberately low-cardinality (event type only — never service or host
names) so scraping doesn't leak topology. Uses its own registry, not the global
default.

### SSE event stream (`GET /events`)

`GET /events` replays the bus backlog on connect and then streams live events as
Server-Sent Events (`data: <json>`). One-way, bearer-authed, reconnects natively.
A periodic `: keep-alive` comment stops idle proxies closing the connection.
`shuttle events` is the CLI consumer.

## Web UI

`web/` is a Vite + TypeScript + Tailwind v4 + Radix read-only dashboard embedded
in the binary via `//go:embed` behind the `embedui` build tag. Built with
`make build-ui`; a plain `go build ./...` needs no `web/dist`. Served under `/ui/`
(unauthenticated static bundle — the browser app authenticates its own API calls
with a bearer token stored in `localStorage`). Uses `@microsoft/fetch-event-source`
for the SSE events view (since `EventSource` cannot set headers). Mutations
(deploy/rollback/prune) are deliberately not in v1.

## Security model

- **Agent auth** (`internal/mtls`, `internal/token`) — two options over TLS 1.3:
  - *mTLS:* `grpc_tls_cert/key/ca` set → mutual cert verification; the agent
    presents a client cert.
  - *Token enrollment:* `grpc_tls_cert/key` (server TLS) + `agent_token_auth` →
    the agent verifies the orchestrator and presents a host-scoped bearer token
    (minted by `shuttle enroll`, stored hashed, revocable). No per-agent certs.

  Insecure transport is dev-only and logs a warning.
- **Webhook HMAC** — `X-Hub-Signature-256` over the raw body, plus a nonce
  replay guard (10-minute TTL) so a captured request can't be replayed.
- **HTTP bearer token** — static token from config guards the control-plane
  endpoints. OIDC is planned.
- **Secret scoping** — agents receive only the env keys a service declares in its
  `env_schema`.

For the rationale behind each of these choices, see [CLAUDE.md](../CLAUDE.md).
