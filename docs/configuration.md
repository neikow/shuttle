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

### Flag fallbacks

`--addr`, `--http-addr`, and `--data-dir` fill in the corresponding config keys
only when they are empty in the file.

## Secrets providers

`secrets_provider: infisical` reads universal-auth credentials from the
environment:

- `INFISICAL_CLIENT_ID`
- `INFISICAL_CLIENT_SECRET`
- `INFISICAL_PROJECT_ID`

The orchestrator fetches secrets and passes a service only the keys listed in its
`env_schema` (see [iac-repo.md](iac-repo.md)). `none` (the default) means no
secret injection.

## mTLS certificates

`make certs` generates a dev CA plus orchestrator and agent certs under `./certs`
(gitignored). The orchestrator cert carries SANs `DNS:orchestrator,DNS:localhost,
IP:127.0.0.1`; the agent's `--server-name` (default `orchestrator`) must match
one of them. For production, issue certs from your own CA with the same SAN
discipline. See [operations.md](operations.md).
