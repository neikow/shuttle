# Operations

How to run Shuttle: the dev stack, mTLS, real hosts, the Synology target, and
releases.

## Local dev stack

`deploy/docker-compose.yml` brings up orchestrator + agent + Caddy:

```sh
docker compose -f deploy/docker-compose.yml up --build
```

- HTTP control plane: `:8080`
- gRPC (agents dial in): `:9090`
- Caddy admin API: `:2019`

The agent mounts the Docker socket so it can run compose deploys, and dials the
orchestrator over an **insecure** gRPC channel (dev only).

### With mTLS

```sh
make certs
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.mtls.yml up --build
```

The overlay swaps in `config.mtls.example.yml` (which sets `grpc_tls_*`) and
passes the agent its client cert/key/CA. The orchestrator then requires and
verifies client certs; an insecure agent is rejected.

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
| `--cert` / `--key` / `--ca` | — | mTLS material (enables mTLS when set). |
| `--server-name` | `orchestrator` | Expected SAN on the orchestrator cert. |

## mTLS certificates

`make certs` writes a dev CA + orchestrator + agent certs to `./certs`
(gitignored). The orchestrator cert carries SANs
`DNS:orchestrator,DNS:localhost,IP:127.0.0.1`; the agent's `--server-name` must
match one. For production, issue certs from your own CA with matching SANs and
distribute the agent cert/key/CA to each host. mTLS is **on** only when the
orchestrator config sets all three `grpc_tls_*` keys.

## Synology DSM target

A Synology NAS runs the agent natively against **Container Manager** (DSM 7.2+):

```sh
shuttle agent --driver synology --orchestrator … --host nas1
```

The `synology` driver invokes `/usr/local/bin/docker` (where DSM installs the CLI
and where Task Scheduler's minimal `PATH` can't find it). Full install guide and
a boot-up task script: [`deploy/synology/README.md`](../deploy/synology/README.md).

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
