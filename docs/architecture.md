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
5. **Ingress** (`caddy.go`) — derives routes from service `domains` + healthcheck
   ports and pushes a full config to Caddy's Admin API on each reconcile.
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

The agent holds no durable state and no secrets at rest — everything it needs
arrives on the stream per deploy.

## The transport: one bidirectional stream

`AgentService.Register` is a single gRPC bidi stream (`proto/shuttle/v1/agent.proto`):

- **Up (agent → orchestrator):** `AgentEvent` = register | heartbeat |
  deploy_result | container_state.
- **Down (orchestrator → agent):** `OrchestratorCommand` = deploy | rollback |
  caddy_config.

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

## Deploy triggers

| Trigger | Path |
|---------|------|
| Git push | `POST /webhook` (HMAC) → async `Reconcile` |
| Manual | `POST /deploy/{service}` → `DeployAtSHA` at HEAD |
| Rollback | `POST /rollback` → `RollbackTarget` → `DeployAtSHA` |
| Drift | `DriftReconciler` tick → `Reconcile` / `ForceDeploy` |

## Security model

- **gRPC mTLS** (`internal/mtls`) — TLS 1.3, mutual cert verification. Enabled
  when the orchestrator config sets `grpc_tls_cert/key/ca`; the agent presents
  its client cert. Insecure transport is dev-only and logs a warning.
- **Webhook HMAC** — `X-Hub-Signature-256` over the raw body, plus a nonce
  replay guard (10-minute TTL) so a captured request can't be replayed.
- **HTTP bearer token** — static token from config guards the control-plane
  endpoints. OIDC is planned.
- **Secret scoping** — agents receive only the env keys a service declares in its
  `env_schema`.

For the rationale behind each of these choices, see [CLAUDE.md](../CLAUDE.md).
