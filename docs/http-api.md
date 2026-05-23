# HTTP API reference

The orchestrator serves a small control plane on `http_addr` (default `:8080`).
All responses are JSON.

| Method & path | Auth |
|---------------|------|
| `GET  /healthz` | none |
| `GET  /deploys` | bearer |
| `POST /deploy/{service}` | bearer |
| `POST /rollback` | bearer |
| `POST /webhook` | HMAC |

**Bearer auth:** send `Authorization: Bearer <bearer_token>`. A missing or wrong
token returns `401`.

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

## `POST /webhook`

Git-push trigger. Enabled only when `repo_url` + `webhook_secret` are configured.

- Body: the raw webhook payload.
- Header: `X-Hub-Signature-256: sha256=<hex HMAC of the body using webhook_secret>`.

The handler verifies the HMAC and rejects replays (a nonce is remembered for 10
minutes). On success it kicks off an async reconcile and returns promptly.
`examples/deploy-workflow.yml` is a drop-in GitHub Actions workflow that signs
and posts this request.

## Status codes

| Code | Meaning |
|------|---------|
| `200` | OK (healthz, list) |
| `202` | Deploy/rollback accepted and queued |
| `400` | Missing required parameter |
| `401` | Bad/missing bearer token |
| `409` | No rollback target available |
| `502` | Could not reach the target agent |
