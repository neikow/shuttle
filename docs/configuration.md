# Configuration reference

Shuttle uses a **two-tier config split**:

| Tier | File | Where | When it takes effect |
|------|------|--------|----------------------|
| Bootstrap | `config.yml` | Orchestrator server | Restart required |
| Repo-managed | `orchestrator.yaml` | IaC git repo | Next reconcile after push |

`config.yml` holds the secrets needed to *start* the orchestrator — bearer
token, repo URL, webhook secret, TLS keypairs, gRPC/HTTP addresses. These cannot
live in git because the orchestrator must read them before it can clone the repo.

`orchestrator.yaml` in the IaC repo holds settings that change without
reinstalling — Caddy config, secrets paths, git credentials. Keys present in
`orchestrator.yaml` override their `config.yml` counterparts on every reconcile.
If the file is absent, config.yml values stand. A parse error is logged and the
old values are kept — a bad commit never blocks deploys.

`shuttle init` generates both files interactively (see
[operations.md](operations.md#bootstrap-with-shuttle-init)).

---

The orchestrator reads a YAML config file (`--config`, default `config.yml`).
Parsing is **strict** — unknown keys are rejected. `bearer_token` is required;
everything else has a default or disables a feature when empty.

See `deploy/config.example.yml` (insecure dev) and `deploy/config.mtls.example.yml`
(mTLS) for ready-to-edit templates.

## Keys

| Key | Default | Purpose |
|-----|---------|---------|
| `bearer_token` | — (**required**) | Static token for HTTP control-plane auth. |
| `grpc_addr` | `:9090` | gRPC listen address (agents dial here). |
| `http_addr` | `:8080` | HTTP control-plane listen address. |
| `data_dir` | `./data` | Holds the SQLite ledger (`shuttle.db`) and, by default, the synced repo. |
| `repo_url` | — | IaC git repo URL. Set with `webhook_secret` to enable git sync. |
| `repo_branch` | `main` | Branch to track. |
| `repo_dir` | `<data_dir>/repo` | Working copy location. |
| `webhook_secret` | — | HMAC secret for `POST /webhook`. Required to enable the webhook + reconcile loop. |
| `caddy_admin_url` | — | Caddy Admin API URL (e.g. `http://caddy:2019`). Empty disables route push. |
| `https_redirect` | `false` | When true, Caddy serves `:443` only and 308-redirects `:80` → HTTPS. When false, `:80` is served plaintext (no redirect). |
| `secrets_provider` | `none` | `infisical` or `none`. |
| `secrets_base_path` | `/shared` | Shared Infisical folder merged under every service. Must be absolute. |
| `secrets_path_template` | — | Per-service folder derived from name, e.g. `/services/{service}` (`{service}` is substituted). Must be absolute. A service's `secret_path` overrides it. |
| `infisical_webhook_secret` | — | HMAC secret for `POST /webhook/infisical`. Required to enable Infisical secret-change webhooks. |
| `infisical_webhook_debounce` | `5s` | How long to coalesce a burst of Infisical changes before triggering a redeploy. Accepts Go duration syntax (`5s`, `1m`, …). |
| `infisical_poll_interval` | — | Enable periodic Infisical secret fingerprint polling as a fallback when webhooks aren't delivered. Accepts Go duration syntax (`1m`, `5m`, …). Empty disables polling. |
| `git_credentials` | — | List of per-repo/org HTTPS token credentials. See [Git credentials](#git-credentials) below. |
| `grpc_tls_cert` / `grpc_tls_key` | — | Orchestrator TLS keypair. Both set → the orchestrator serves TLS. |
| `grpc_tls_ca` | — | Added to cert+key → require + verify client certs (mutual TLS). |
| `agent_token_auth` | `false` | Require agents to present a valid enrollment token to register (see [operations.md](operations.md#enrolling-agents-with-tokens)). |
| `advertise_addr` | `grpc_addr` | gRPC `host:port` baked into the command `shuttle enroll` prints. |
| `advertise_server_name` | — | Orchestrator cert SAN added to the enrollment command when TLS is on. |
| `webhook_rate_limit_per_minute` | `120` | Per-IP cap on the unauthenticated endpoints (webhooks + `/enroll/redeem`). A negative value disables limiting. |
| `metrics_require_auth` | `false` | Gate `GET /metrics` behind the read role. Default keeps the standard unauthenticated scrape model; set true when `/metrics` is reachable from an untrusted network. |
| `oidc` | — | Per-user OpenID Connect auth. Set `oidc.issuer` to enable. See [OIDC](#oidc-per-user-auth) below. |

### Feature gating

- **Git sync + webhook + drift reconcile** turn on only when *both* `repo_url`
  and `webhook_secret` are set.
- **Caddy route push** turns on when `caddy_admin_url` is set.
- **gRPC transport:** `cert`+`key` → server TLS; adding `ca` → mutual TLS;
  neither → insecure (a warning is logged).
- **Token auth** turns on with `agent_token_auth: true`. Pair it with server TLS
  (`cert`+`key`) so tokens are encrypted in transit. Enrollment endpoints
  (`GET /hosts`, `POST /enroll`) are served only when token auth *and* git sync
  are both configured.
- **Infisical webhook** turns on when `infisical_webhook_secret` is set.
- **Infisical polling** turns on when `infisical_poll_interval` is set.
- **OIDC auth** turns on when `oidc.issuer` is set (discovery happens at startup).

### OIDC (per-user auth)

`oidc:` adds per-user OpenID Connect login on top of the static `bearer_token`
and the named control tokens (`shuttle token`). A presented JWT is verified
against the issuer's published keys and mapped to a control-plane role, so OIDC
users flow through the same `read < deploy < admin` model — and the caller's
identity becomes the audit actor. The static bearer stays the break-glass admin;
OIDC is purely additive.

| Sub-key | Default | Purpose |
|---------|---------|---------|
| `issuer` | — | OIDC issuer URL (e.g. `https://accounts.google.com`, or a self-hosted Dex/Keycloak). Setting it enables OIDC. Its `/.well-known/openid-configuration` is fetched **at startup** — an unreachable issuer fails the orchestrator at boot. |
| `audience` | — (**required** with issuer) | Expected `aud` claim — the client ID registered with the IdP for Shuttle. |
| `roles_claim` | `groups` | Token claim read for role mapping. Its value may be a string or a list of strings. |
| `role_mapping` | — (**required** with issuer) | Maps a value found in `roles_claim` to a role (`read`/`deploy`/`admin`). The highest-ranked matched role wins; a token mapping to nothing is authenticated but **403**. |
| `username_claim` | `sub` | Claim used as the caller's identity (the audit actor). |

```yaml
oidc:
  issuer: "https://keycloak.example.com/realms/shuttle"
  audience: "shuttle"
  roles_claim: "groups"
  username_claim: "email"
  role_mapping:
    shuttle-admins: admin
    shuttle-deployers: deploy
    shuttle-viewers: read
```

Your IdP must include Shuttle's `audience` in the token's `aud` and emit the
configured `roles_claim`. OIDC verification only runs on JWT-shaped tokens, so
the static bearer and control tokens are never affected.

### Flag fallbacks

`--addr`, `--http-addr`, and `--data-dir` fill in the corresponding config keys
only when they are empty in the file.

## Secrets providers

`secrets_provider: infisical` reads universal-auth credentials from the
environment:

| Env var | Required | Default | Purpose |
|---------|----------|---------|---------|
| `INFISICAL_CLIENT_ID` | yes | — | Universal-auth client ID. |
| `INFISICAL_CLIENT_SECRET` | yes | — | Universal-auth client secret. |
| `INFISICAL_PROJECT_ID` | yes | — | Project to read secrets from. |
| `INFISICAL_ENV` | no | `production` | Default environment slug, used when a service has no `env_from`. |
| `INFISICAL_SECRET_PATH` | no | `/` | Provider fallback folder. Superseded per service by `secrets_base_path` / `secrets_path_template` / `secret_path`. |
| `INFISICAL_SITE_URL` | no | Infisical Cloud | Self-hosted Infisical base URL. |

For each deploy the orchestrator fetches secrets along **two axes** and writes
the service's `.env`:

- **Environment** ← the service's **`env_from`** (overrides `INFISICAL_ENV`).
- **Folder** ← a **shared base** (`secrets_base_path`, default `/shared`) merged
  with the service's **own folder**. The service folder is its `secret_path` if
  set, else `secrets_path_template` with `{service}` substituted, else the base.

Both folders are read from the same environment and merged, with the
service-specific folder winning on key conflicts; **`env_schema`** then filters
which keys reach the service (see [iac-repo.md](iac-repo.md)). All folder paths
must be absolute. `none` (the default provider) means no secret injection.

Example: `secrets_base_path: /shared`, `secrets_path_template: /services/{service}`,
and a service `api` with `env_from: production` reads `production` secrets from
`/shared` + `/services/api`, the latter overriding the former.

## Git credentials

`git_credentials` allows the orchestrator to authenticate to private HTTPS git
repos (the IaC repo or remote compose pointers) using tokens stored in Infisical.
Each entry specifies the repo prefix and where to fetch the token:

```yaml
git_credentials:
  - repo_prefix: github.com/myorg   # no scheme; matches any HTTPS URL with this prefix
    infisical_key: GITHUB_TOKEN     # secret key to fetch from Infisical
    infisical_env: production       # optional; overrides INFISICAL_ENV
    infisical_path: /shared         # optional; Infisical folder for this key
```

The `repo_prefix` must not include the scheme (`https://` is stripped). On each
git operation the orchestrator fetches the token from Infisical and passes it via
`git -c http.<url>.extraHeader=Authorization:Bearer <token>`. Requires
`secrets_provider: infisical`.

`shuttle check` reports the status of every configured credential (whether the
token resolved successfully) alongside the service validation results.

## mTLS certificates

`make certs` generates a dev CA plus orchestrator and agent certs under `./certs`
(gitignored). The orchestrator cert carries SANs `DNS:orchestrator,DNS:localhost,
IP:127.0.0.1`; the agent's `--server-name` (default `orchestrator`) must match
one of them. For production, issue certs from your own CA with the same SAN
discipline. See [operations.md](operations.md).

## Repo-managed config (`orchestrator.yaml`)

Place this file in the root of the IaC git repo to override the bootstrap
settings without restarting the orchestrator. Changes take effect on the next
reconcile after the commit lands on the tracked branch.

```yaml
# orchestrator.yaml — checked into the IaC repo, not the server.
# All keys are optional; omit a key to keep the config.yml value.

caddy_admin_url: "http://caddy:2019"
https_redirect: false

secrets_base_path: "/shared"
secrets_path_template: "/services/{service}"

git_credentials:
  - repo_prefix: github.com/myorg
    infisical_key: GITHUB_TOKEN
    infisical_env: production
    infisical_path: /shared
```

| Key | Overrides | Purpose |
|-----|-----------|---------|
| `caddy_admin_url` | `caddy_admin_url` in `config.yml` | Caddy Admin API URL. |
| `https_redirect` | `https_redirect` in `config.yml` | HTTPS redirect toggle. Explicitly set to `false` to force plaintext on `:80`; omit to keep the bootstrap value. |
| `secrets_base_path` | `secrets_base_path` in `config.yml` | Shared Infisical folder. |
| `secrets_path_template` | `secrets_path_template` in `config.yml` | Per-service Infisical folder template. |
| `git_credentials` | `git_credentials` in `config.yml` | Per-repo HTTPS tokens from Infisical. |

**Parse errors** (unknown keys, bad YAML) are logged; old settings are kept. The
orchestrator never stalls a deploy waiting for a fixed config file.

`shuttle init` scaffolds a commented `orchestrator.yaml` alongside the rest of
the IaC repo. See [iac-repo.md](iac-repo.md#orchestratoryaml) for the full
layout.
