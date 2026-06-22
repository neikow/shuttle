# Operations

How to run Shuttle: the dev cluster, mTLS, real hosts, the Synology target, and
releases.

## Local dev cluster

`make dev-up` brings up a complete cluster: the orchestrator (with the embedded
web UI) wired to a local git IaC repo, plus two **simulated remote hosts** —
each an isolated Docker-in-Docker engine running a shuttle agent that
self-enrolls over the control plane and deploys into its own engine. This
mirrors real managed hosts far better than sharing the host's Docker socket.

```sh
make dev-up      # build + start; UI at http://localhost:8080/ui/ (token: test-bearer)
make dev-logs    # follow every container's logs
make dev-down    # stop + remove volumes and the seeded repo
```

- HTTP control plane + UI: `:8080`
- gRPC (agents dial in): `:9090`

The IaC repo is seeded to `.dev-cluster/iac`; edit it and commit, and the
reconciler deploys the change within ~60s. The cluster runs over plaintext with
a fixed bearer token — **dev only** (the hosts self-enroll via `POST /enroll`,
which also exercises the enrollment flow end-to-end).

Files: `deploy/docker-compose.dev.yml` + `deploy/dev/` (Dockerfile, host
entrypoint, config, seed repo).

### With mTLS

mTLS secures the orchestrator↔agent gRPC link in real deployments (the dev
cluster above runs plaintext). Generate dev material with `make certs`, then set
`grpc_tls_cert`/`grpc_tls_key` (server TLS) or add `grpc_tls_ca` (mutual TLS) in
the orchestrator config — see the commented keys in `deploy/config.example.yml`.
Agents then dial with `--cert/--key/--ca`. With mutual TLS the orchestrator
requires and verifies client certs; an insecure agent is rejected.

## Bootstrap with `shuttle init`

`shuttle init` is an interactive, **secure-by-default** wizard that sets up a
complete orchestrator environment in one command. Run it once on the orchestrator
server:

```sh
shuttle init [--dir /etc/shuttle]
```

Press Enter through it and you get a secure setup; pick non-default answers for
the advanced paths. It prompts for:

- Orchestrator addresses and the externally reachable control URL
- Agent transport: **token enrollment over TLS** (default), mutual TLS, or
  insecure. The token path can **generate a self-signed orchestrator cert** for
  you (agents pin it on first use and receive it at enrollment — no CA to copy)
- Bearer token and webhook secret (auto-generated if left blank, written 0600)
- The IaC repo: a **starter** example service, an **empty** scaffold, or an
  **existing** remote URL (no local scaffold)
- Secrets provider (none or Infisical, with credentials written to `.env`)
- Whether to write GitHub Actions workflows (deploy-on-push + PR plan-comment)

Everything lands in **one project directory** (`--dir`, default `.`) — the
bootstrap files and the IaC repo share it, with no nested sub-folder. Only the
TLS material is nested, under `certs/`. The scaffolded `.gitignore` keeps the
sensitive files out of git.

| File | Location | Notes |
|------|----------|-------|
| `config.yml` | `--dir` (default `.`) | Mode 0600; bootstrap secrets; gitignored |
| `.env` | `--dir` | Mode 0600; Infisical creds; loaded at startup; gitignored |
| `certs/` | `--dir/certs/` | Self-signed orchestrator cert/key (token path, if generated); key 0600; gitignored |
| IaC repo | `--dir` | `hosts.yaml`, `services/`, `orchestrator.yaml`, `.gitignore`, optional `.github/workflows/` — committed here |

Bootstrapping into one directory matches how editors (and the
[VS Code extension](editor.md)) open a project root: `hosts.yaml` and
`services/` sit at the top level, while `config.yml`, `.env`, and `certs/` are
present but gitignored. A **starter** repo with no remote points `repo_url` at
the local repo via `file://`, so the orchestrator drives it directly — a real
first deploy with nothing to push. Running `shuttle init` a second time in the
same directory is safe: existing files (including a real cert) are never
overwritten.

## Running on real hosts

### Orchestrator

```sh
shuttle orchestrator --config /etc/shuttle/config.yml
```

A systemd unit template is at `deploy/systemd/shuttle-orchestrator.service`.
Provide a `config.yml` (see [configuration.md](configuration.md)). The data dir
holds the SQLite ledger — back it up to preserve deploy history.

### Agent

```sh
shuttle agent \
  --orchestrator orch.example.com:9090 \
  --host web1 \
  --work-dir /var/lib/shuttle/agent \
  --cert /etc/shuttle/certs/agent.crt \
  --key  /etc/shuttle/certs/agent.key \
  --ca   /etc/shuttle/certs/ca.crt
```

`--host` must match a name in the IaC repo's `hosts.yaml`. Drop the cert flags
for an insecure dev link. Templates: `deploy/systemd/shuttle-agent.service`,
`deploy/launchd/` (macOS).

#### Agent flags

| Flag | Default | Purpose |
|------|---------|---------|
| `--orchestrator` | — (required) | Orchestrator gRPC `host:port`. |
| `--host` | — (required) | Host name; must match `hosts.yaml`. |
| `--work-dir` | `./agent-work` | Where rendered compose files are written. |
| `--driver` | `compose` | `compose` or `synology`. |
| `--docker-bin` | — | Override the Docker executable path. |
| `--caddy-image` | `ghcr.io/neikow/shuttle-caddy:<version>` | Caddy ingress sidecar image. The default bundles the OVH DNS plugin for [DNS-challenge certs](iac-repo#dnsyml-optional); override with a custom `xcaddy` build for another DNS provider. |
| `--cert` / `--key` / `--ca` | — | TLS material. cert+key+ca → mutual TLS; ca only → verify the orchestrator and authenticate by token. |
| `--server-name` | `orchestrator` | Expected SAN on the orchestrator cert. |
| `--token` | — | Enrollment token (from `shuttle enroll`). |

## Enrolling agents with tokens

Instead of issuing a client cert per agent, the orchestrator can mint a
host-scoped **enrollment token** and hand you a ready-to-run agent command. This
is the simplest way to bring up a new host.

### Orchestrator setup

Set in `config.yml`:

```yaml
agent_token_auth: true
grpc_tls_cert: /etc/shuttle/certs/orchestrator.crt   # serve TLS so the token
grpc_tls_key:  /etc/shuttle/certs/orchestrator.key   # is encrypted in transit
advertise_addr: orchestrator.example.com:9090        # what agents dial
advertise_server_name: orchestrator                  # SAN on the cert above
```

With only `cert`+`key` (no `ca`) the orchestrator serves TLS and authenticates
agents by token — no per-agent certs. Token auth without TLS works but sends the
token in cleartext (a warning is logged); don't do that in production.

### Enroll a host

`shuttle enroll` talks to the running orchestrator's control plane, lists the
hosts from the IaC repo, and prints the agent command:

```sh
shuttle enroll --url https://orchestrator.example.com:8080 --token "$BEARER_TOKEN"
```

```
Available hosts:
  1) web1  (region=eu-west, role=edge)
Select a host [1]: 1

Enrolled host "web1" (token id 1779…).
Run the agent with:

  shuttle agent --orchestrator orchestrator.example.com:9090 --host web1 \
    --token g5R8…63PE --server-name orchestrator
```

Pass `--host web1` to skip the interactive picker. If the orchestrator uses a
private CA, add `--ca <path-to-ca.crt>` to the agent command so it can verify the
server. The token is shown once — treat it as a secret. Tokens are stored hashed
and scoped to the host; a token presented for any other host is rejected.

## mTLS certificates

`make certs` writes a dev CA + orchestrator + agent certs to `./certs`
(gitignored). The orchestrator cert carries SANs
`DNS:orchestrator,DNS:localhost,IP:127.0.0.1`; the agent's `--server-name` must
match one. For production, issue certs from your own CA with matching SANs and
distribute the agent cert/key/CA to each host. mTLS is **on** only when the
orchestrator config sets all three `grpc_tls_*` keys.

## Backups

Shuttle backs up a service's **persistent data** (Docker volumes or a postgres
dump), separate from `shuttle backup`, which snapshots the deploy *ledger*.

Two pieces configure it:

1. **Per-service policy** in the IaC repo (`backup:` in the service YAML) — the
   engine (`volume`/`postgres`), schedule, retention, and `before_deploy`. See
   [iac-repo.md](iac-repo.md).
2. **Host wiring** in `config.yml` (`backups:`) — the backend credentials and the
   store/target defaults services inherit. See
   [configuration.md](configuration.md#backups).

```yaml
# config.yml
backups:
  default_store: restic
  default_target: "s3:s3.amazonaws.com/my-bucket"
  env:
    - { key: RESTIC_PASSWORD, infisical_key: RESTIC_PASSWORD }
    - { key: AWS_ACCESS_KEY_ID, infisical_key: AWS_ACCESS_KEY_ID }
    - { key: AWS_SECRET_ACCESS_KEY, infisical_key: AWS_SECRET_ACCESS_KEY }
```

```yaml
# services/db/db.yaml
name: db
host: data1
backup:
  engine: postgres
  db_service: db          # compose service of the database container
  db_user: postgres
  schedule: daily
  before_deploy: true
  retention: { keep_daily: 7, keep_weekly: 4 }
```

**When backups run:**

- **Scheduled** — a scheduler ticks (`backups.poll_interval`, default 5m) and
  dispatches a service's backup once its `schedule` interval has elapsed.
- **Before a deploy** — with `before_deploy: true`, a best-effort snapshot is
  taken before each deploy/rollback (a 5-minute cooldown prevents a crash-loop
  drift heal from snapshotting every tick).
- **Manually** — on demand from the control plane.

**Manage from the CLI** (talks to a running orchestrator):

```sh
shuttle backup-service db --url https://orchestrator:8080 --token $SHUTTLE_TOKEN
shuttle backups --service db --url … --token …
shuttle restore-service db --yes --url … --token …          # latest successful backup
shuttle restore-service db --backup-id <id> --yes --url … --token …
```

`restore-service` is **destructive** (it overwrites live data) and admin-tier; it
confirms unless `--yes` is passed. Restore is always cold: the service is stopped,
the data is restored, then the service is started again.

**Engines:**

- `volume` — tars the project's named Docker volumes. With the restic store the
  tars are uncompressed so restic dedups effectively across snapshots.
- `postgres` — runs `pg_dump` (one database) or `pg_dumpall` (all + roles) inside
  the DB container; `PGPASSWORD` is pulled automatically from the service's
  secrets. The agent host needs the database client tools only inside the
  container (it `docker exec`s them), not on the host.

The agent needs Docker (for the helper containers that tar volumes and run
`restic/restic`); nothing else is installed on the host. Backend credentials are
passed to the helper containers via environment passthrough — never written to
disk or the process arguments.

## Synology DSM target

A Synology NAS runs the agent natively against **Container Manager** (DSM 7.2+):

```sh
shuttle agent --driver synology --orchestrator … --host nas1
```

The `synology` driver invokes `/usr/local/bin/docker` (where DSM installs the CLI
and where Task Scheduler's minimal `PATH` can't find it). Full install guide and
a boot-up task script: [`deploy/synology/README.md`](https://github.com/neikow/shuttle/blob/main/deploy/synology/README.md).

## Webhooks from CI

`examples/deploy-workflow.yml` is a drop-in GitHub Actions workflow: it HMAC-signs
the payload and `POST`s it to `/webhook`. Configure repo variable `SHUTTLE_URL`
and secret `SHUTTLE_WEBHOOK_SECRET` (matching the orchestrator's `webhook_secret`).

## Releases

Pushing a `v*` tag triggers `.github/workflows/release.yml`, which runs GoReleaser
to publish:

- archives for linux/darwin × amd64/arm64 + `checksums.txt` on a GitHub release,
- multi-arch images `ghcr.io/neikow/shuttle:<version>` and `:latest`.

Validate config locally with `goreleaser check`; snapshot-build without
publishing via `goreleaser release --snapshot --clean`.
