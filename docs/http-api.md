# HTTP API reference

The orchestrator serves a small control plane on `http_addr` (default `:8080`).
All responses are JSON.

| Method & path | Auth |
|---------------|------|
| `GET  /healthz` | none |
| `GET  /metrics` | none |
| `GET  /deploys` | bearer |
| `POST /deploy/{service}` | bearer |
| `POST /rollback` | bearer |
| `GET  /overview` | bearer |
| `GET  /plan` | bearer |
| `GET  /check` | bearer |
| `GET  /events` | bearer |
| `GET  /hosts` | bearer |
| `POST /enroll` | bearer |
| `POST /prune` | bearer |
| `POST /webhook` | HMAC |
| `POST /webhook/infisical` | HMAC |
| `POST /webhook/repo/{id}` | ID |
| `POST /webhooks/repo` | bearer |
| `GET  /webhooks/repo` | bearer |
| `DELETE /webhooks/repo/{id}` | bearer |

**Bearer auth:** send `Authorization: Bearer <bearer_token>`. A missing or wrong
token returns `401`.

`GET /hosts` and `POST /enroll` exist only when `agent_token_auth` and git sync
are configured; see [operations.md](operations.md#enrolling-agents-with-tokens).

---

## `GET /healthz`

Liveness probe. Always `200`:

```json
{ "status": "ok" }
```

## `GET /deploys`

List ledger records, newest first.

| Query | Default | Notes |
|-------|---------|-------|
| `service` | (all) | Filter to one service. |
| `limit` | `50` | Clamped to `1..200`. |

```sh
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8080/deploys?service=whoami&limit=20"
```

Returns an array of deploy records (`deploy_id`, `service`, `host`, `sha`,
`status`, `triggered_by`, timestamps).

## `POST /deploy/{service}`

Deploy a service at a specific commit.

| Query | Required | Notes |
|-------|----------|-------|
| `sha` | yes | Commit to deploy. |
| `host` | only in legacy mode | Required when no git sync is configured. |

When git sync is configured (the normal case) the orchestrator checks out `sha`,
renders the real compose + env, and dispatches to the service's host:

```sh
curl -X POST -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8080/deploy/whoami?sha=abc123"
```

`202 Accepted`:

```json
{ "deploy_id": "…", "host": "web1" }
```

Without git sync, the orchestrator only sends a bare deploy command (the agent
must already have the project on disk) and `host` is required.

## `POST /rollback`

Roll a service back to an earlier successful deploy.

| Query | Default | Notes |
|-------|---------|-------|
| `service` | — (required) | Service to roll back. |
| `steps` | `1` | How many deploys back to go. |

```sh
curl -X POST -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8080/rollback?service=whoami&steps=1"
```

The orchestrator resolves the target SHA from the ledger (`409` if there is no
such target), renders that revision's compose, and dispatches it. `202 Accepted`
with `{ "deploy_id", "host" }`.

## `GET /hosts`

List the hosts declared in the IaC repo (used by `shuttle enroll`):

```json
[ { "name": "web1", "labels": { "region": "eu-west" } } ]
```

## `POST /enroll`

Mint a host-scoped agent enrollment token.

- Body: `{ "host": "web1" }` — must reference a host from `GET /hosts`.

`201 Created`:

```json
{
  "id": "1779…",
  "host": "web1",
  "token": "g5R8…63PE",
  "command": "shuttle agent --orchestrator … --host web1 --token g5R8…63PE --server-name orchestrator",
  "tls": true
}
```

The plaintext `token` is returned once; only its hash is stored. An unknown host
returns `404`.

## `POST /webhook`

Git-push trigger. Enabled only when `repo_url` + `webhook_secret` are configured.

- Body: the raw webhook payload.
- Header: `X-Hub-Signature-256: sha256=<hex HMAC of the body using webhook_secret>`.

The handler verifies the HMAC and rejects replays (a nonce is remembered for 10
minutes). On success it kicks off an async reconcile and returns promptly.
`examples/deploy-workflow.yml` is a drop-in GitHub Actions workflow that signs
and posts this request.

## `GET /metrics`

Prometheus exposition format. Unauthed (standard Prometheus scrape model).
Labels are deliberately low-cardinality (event type only — never service or host
names) so scraping doesn't leak topology.

Metrics exposed:

| Metric | Type | Description |
|--------|------|-------------|
| `shuttle_events_total{type}` | counter | Total events published to the event bus |
| `shuttle_deploy_duration_seconds` | histogram | Deploy duration, timed from `deploy.queued` to terminal event |
| `shuttle_connected_agents` | gauge | Number of agents currently connected |
| `shuttle_event_bus_dropped_total` | counter | Events dropped due to slow subscribers |

## `GET /overview`

Single-screen snapshot merging agent liveness with the latest reported container
state per service. A host appears even if offline, as long as it has any known
services.

```json
{
  "hosts": [
    {
      "name": "web1",
      "connected": true,
      "services": [
        { "name": "whoami", "status": "running", "sha": "abc123", "containers": 1 }
      ]
    }
  ]
}
```

## `GET /plan`

Read-only desired-vs-actual diff. Syncs the repo (or a specific `?ref=`) and
diffs every service against `ledger.CurrentSHAs`. Dispatches nothing.

| Query | Default | Notes |
|-------|---------|-------|
| `ref` | HEAD | Branch, tag, `refs/pull/N/head`, or SHA to diff against. Uses an isolated temp checkout so the working tree is untouched. |

```sh
# Current branch vs live ledger
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/plan

# Preview a PR branch
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8080/plan?ref=refs/pull/7/head"
```

Returns a per-service status (`create`/`update`/`unchanged`/`remove`) and the
current + desired SHAs.

## `GET /check`

Read-only validation: syncs the repo (or a specific `?ref=`) and verifies that
every service's `env_schema` keys resolve in the secrets provider. Collects all
problems (no fail-fast), dispatches nothing.

| Query | Default | Notes |
|-------|---------|-------|
| `ref` | HEAD | Same isolated-checkout semantics as `/plan`. |

```sh
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/check
```

Returns `{ "sha": "…", "ok": true, "services": { … }, "git_credentials": [ … ] }`.

## `GET /events`

Server-Sent Events stream of orchestrator events. On connect, the handler
replays the recent event backlog, then forwards live events.

Event types: `deploy.queued`, `deploy.succeeded`, `deploy.failed`,
`deploy.rolled_back`, `rollback.queued`, `drift.detected`, `service.removed`,
`volumes.purged`.

```sh
# With the CLI
shuttle events --url http://localhost:8080 --token "$TOKEN"

# Raw SSE
curl -H "Authorization: Bearer $TOKEN" -N http://localhost:8080/events
```

Each frame: `data: <json>` where the JSON carries a `type` field for filtering.
A slow reader has events dropped (not backpressured) so the deploy path is never
blocked. A periodic `: keep-alive` comment prevents idle proxies from closing the
connection.

Note: `EventSource` cannot set headers, so browser clients must use
`@microsoft/fetch-event-source` or equivalent.

## `POST /webhook/infisical`

Infisical secret-change trigger. Enabled only when `infisical_webhook_secret` is
configured.

- Body: raw Infisical webhook payload.
- Header: `x-infisical-signature: t=<ts>,v1=<hmac>` over `<ts>.<body>`.

On success, only the services reading the changed (env, folder) are redeployed
(non-recursive folder match). A burst of edits is coalesced over
`infisical_webhook_debounce` (default 5 s) before the redeploy fires.

## `POST /webhooks/repo` / `GET /webhooks/repo` / `DELETE /webhooks/repo/{id}`

Manage service-specific deploy webhooks. Each webhook is scoped to one service;
triggering it forces a redeploy of that service.

**Create:**

```sh
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -d '{"service":"whoami"}' \
  http://localhost:8080/webhooks/repo
```

`201 Created`:

```json
{ "id": "a3f8b2…" }
```

The `id` is a 256-bit random string that acts as the secret — no HMAC, no
additional auth on the trigger endpoint.

**List:**

```sh
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/webhooks/repo
```

Returns an array of `{ "id", "service", "created_at" }`.

**Delete:**

```sh
curl -X DELETE -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/webhooks/repo/a3f8b2…
```

`204 No Content`.

## `POST /webhook/repo/{id}`

Trigger a service-specific deploy webhook. No bearer auth — the 256-bit random
`id` is the secret. The payload body is ignored.

```sh
curl -X POST https://orch.example.com:8080/webhook/repo/a3f8b2…
```

`202 Accepted`:

```json
{ "deploy_ids": ["…"] }
```

Use this in external systems (container registries, CI pipelines) to redeploy a
single service without exposing the orchestrator bearer token.

## Status codes

| Code | Meaning |
|------|---------|
| `200` | OK (healthz, list, hosts, plan, check) |
| `201` | Enrollment token or repo webhook created |
| `202` | Deploy/rollback/webhook trigger accepted and queued |
| `204` | Repo webhook deleted |
| `400` | Missing required parameter |
| `401` | Bad/missing bearer token |
| `404` | Unknown host (enroll) or webhook ID not found |
| `409` | No rollback target available |
| `502` | Could not reach the target agent |
