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

## Bootstrap: `shuttle init` + `shuttle orchestrator init`

Bootstrap is split along the same seam as the [two-tier config](configuration.md):
**`shuttle init`** scaffolds the git-managed IaC repo, and **`shuttle
orchestrator init`** generates the server-side config that stays on the box.
Both are **secure-by-default** and short — press Enter through them — and both
take `--advanced` to expose every knob and `--dir` to target a directory (default
`.`). Run them in the same directory:

```sh
shuttle init [--dir /etc/shuttle]               # IaC repo
shuttle orchestrator init [--dir /etc/shuttle]  # server config + TLS
```

### `shuttle init` — the IaC repo

Scaffolds the git-managed side and asks **two** questions (default): a **starter**
example service (`whoami` + a `local` host) or an **empty** scaffold, and the
**CI provider** you'll push to — `none`, **GitHub Actions** (`.github/workflows/`
deploy-on-push + PR plan-comment), or **GitLab CI** (`.gitlab-ci.yml` deploy +
merge-request plan). `--ci github|gitlab|none` pre-answers it non-interactively.
It writes `hosts.yaml`, `services/`, `orchestrator.yaml`, `.gitignore`, the CI
files, then makes an initial commit. `--advanced` additionally prompts for
`orchestrator.yaml` overrides (Caddy admin URL, secret paths).

The repo is **remote-agnostic**: `shuttle init` never touches a git remote (it
may not exist yet). You author on one machine, create an empty repo on your
provider, push, then bootstrap the orchestrator on the server from that URL — see
[the cross-machine flow](#cross-machine-author-here-run-orchestrator-there) below.

### `shuttle orchestrator init` — the server config

Generates `config.yml`, an optional `.env`, and self-signed TLS material under
`certs/`. It asks **two** questions (default): agent transport — **token
enrollment over TLS** (default; generates a self-signed orchestrator cert agents
pin on first use, no CA to copy), mutual TLS, or insecure — and the secrets
provider (none, Infisical, or file). It **auto-detects the scaffolded repo** in
`--dir` to fill `repo_url`: an existing `origin` remote is reused; a local repo
with no remote is driven directly via `file://` — a real first deploy with
nothing to push. Override with `--repo-url`. The bearer token and webhook secret
are auto-generated (written 0600) unless `--advanced` prompts for them, which also
exposes addresses, the advertise URL/SAN, cert paths, and secret paths.

Everything lands in **one project directory** so the bootstrap files and the IaC
repo share it, with no nested sub-folder. Only the TLS material is nested, under
`certs/`. The `.gitignore` from `shuttle init` keeps the sensitive server files
out of git.

| File | Written by | Location | Notes |
|------|-----------|----------|-------|
| `config.yml` | `orchestrator init` | `--dir` (default `.`) | Mode 0600; bootstrap secrets; gitignored |
| `.env` | `orchestrator init` | `--dir` | Mode 0600; Infisical creds; loaded at startup; gitignored |
| `certs/` | `orchestrator init` | `--dir/certs/` | Self-signed orchestrator cert/key (token path, if generated); key 0600; gitignored |
| IaC repo | `init` | `--dir` | `hosts.yaml`, `services/`, `orchestrator.yaml`, `.gitignore`, optional CI (`.github/workflows/` or `.gitlab-ci.yml`) — committed here |

Bootstrapping into one directory matches how editors (and the
[VS Code extension](editor.md)) open a project root: `hosts.yaml` and
`services/` sit at the top level, while `config.yml`, `.env`, and `certs/` are
present but gitignored. Re-running either command in the same directory is safe:
existing files (including a real cert) are never overwritten.

### Cross-machine: author here, run orchestrator there

The two commands run on **different machines** for the common production layout —
you author the IaC repo on your workstation and run the orchestrator on a server.
`shuttle init` is remote-agnostic, so the handoff is plain git:

```sh
# 1. On your workstation — scaffold the repo (pick your CI provider):
shuttle init --ci github          # or: gitlab / none

# 2. Create an empty repo on GitHub/GitLab/…, then publish:
git remote add origin git@github.com:you/iac.git
git push -u origin main

# 3. On the orchestrator server — bootstrap from the remote URL:
shuttle orchestrator init --repo-url https://github.com/you/iac.git
shuttle orchestrator --config config.yml
```

`shuttle orchestrator init` writes `repo_url` into `config.yml`; the orchestrator
clones it on start and reconciles from it. `--repo-url` is also what to use for an
IaC repo you already have (no `shuttle init` needed). When `--dir` instead
contains a freshly scaffolded **local** repo with no remote, `orchestrator init`
auto-detects it and drives it directly over `file://` — the single-machine path,
where steps 2–3 collapse into one `shuttle orchestrator init` (no push).

If the IaC repo is **private**, give the orchestrator a **repo-scoped** token to
clone it — a GitHub fine-grained PAT (Contents: read) or deploy key, a GitLab
project access token (`read_repository`), etc., limited to that one repo rather
than an account/org-wide PAT. Store the token in your secrets provider and
reference it from `orchestrator.yaml` `git_credentials`; see
[Git credentials](configuration.md#git-credentials).

If you scaffolded CI, set the repo variables it expects — `SHUTTLE_URL`,
`SHUTTLE_WEBHOOK_SECRET` (deploy), and `SHUTTLE_TOKEN` (plan) — in your provider's
CI/CD settings so push-to-deploy and PR/MR plan comments work.

## Running on real hosts

### Orchestrator

```sh
shuttle orchestrator --config /etc/shuttle/config.yml
```

A systemd unit template is at `deploy/systemd/shuttle-orchestrator.service`.
Provide a `config.yml` (see [configuration.md](configuration.md)). The data dir
holds the SQLite ledger — back it up to preserve deploy history.

#### Preflight with `shuttle doctor`

Before (or as part of) starting the orchestrator, run a host-level preflight:

```sh
shuttle doctor --config /etc/shuttle/config.yml
```

It checks that `config.yml` parses, the `git` binary is present and the IaC repo
is reachable, Docker is reachable (only needed on agent hosts — use
`--skip-docker` on an orchestrator-only box), the data dir is writable, the gRPC
TLS cert parses and isn't expired (or expiring within `--cert-warn-days`, default
30), and the configured secrets provider can be constructed. It dispatches
nothing and writes no state, so it is safe as a systemd `ExecStartPre=` or a CI
smoke test — a failed check exits non-zero (warnings alone exit 0). For deep
IaC-repo and per-service secret validation, run `shuttle check`.

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
