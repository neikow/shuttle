# Configuration reference

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
