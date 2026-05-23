# Shuttle

Self-hosted, git-driven Infrastructure-as-Code deployment platform. A single Go
binary that watches an IaC git repository and rolls changes out to your own
hosts over Docker Compose — with an append-only deploy ledger, one-command
rollback, drift detection, secrets injection, and automatic Caddy ingress.

Think Portainer's spirit (own your infra, no SaaS), plus full rollback history,
secret management, and multi-host fan-out.

> Status: **v0.1.0** — first release. Core pipeline is working end-to-end.

## How it works

```
   git push                                          docker compose up
  ┌──────────┐   webhook    ┌──────────────┐  gRPC   ┌──────────┐
  │ IaC repo │ ───────────► │ orchestrator │ ──────► │  agent   │ ──► containers
  └──────────┘  (HMAC)      └──────────────┘ (mTLS)  └──────────┘
                                  │  ▲ stream: heartbeats, deploy results,
                                  │  │ container state (drift signal)
                                  ▼
                            SQLite ledger ──► rollback target lookup
                                  │
                                  ▼
                            Caddy Admin API ──► TLS ingress routes
```

- **Orchestrator** is the brain: it pulls the IaC repo, diffs the desired state
  against the deploy ledger, renders each service's compose + env (with secrets),
  and dispatches deploy commands to agents. It also pushes ingress routes to Caddy.
- **Agents** are dumb executors. They dial *out* to the orchestrator (no inbound
  firewall holes), receive rendered compose files, and shell out to
  `docker compose`. They report container state back so the orchestrator can
  detect and heal drift.

See [docs/architecture.md](docs/architecture.md) for the full design and the
rationale behind each choice.

## Quickstart (local dev stack)

Brings up orchestrator + agent + Caddy with Docker Compose:

```sh
docker compose -f deploy/docker-compose.yml up --build
```

The HTTP control plane is on `:8080`, gRPC on `:9090`, Caddy admin on `:2019`.
For a mutual-TLS link between orchestrator and agent:

```sh
make certs
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.mtls.yml up --build
```

## Build & test

```sh
make build          # -> ./shuttle (version stamped from git)
make test           # go test -race ./internal/...
make lint           # golangci-lint (v2)
make proto          # regenerate gen/ from proto/ via buf
make certs          # generate dev mTLS CA + orchestrator + agent certs
```

## Run

```sh
# Orchestrator (reads config.yml; see deploy/config.example.yml)
shuttle orchestrator --config /etc/shuttle/config.yml

# Enroll a host: pick from the IaC repo's hosts, get a ready-to-run agent command
shuttle enroll --url https://orch.example.com:8080 --token "$BEARER_TOKEN"

# Agent (on a managed host; --host must match a name in hosts.yaml)
shuttle agent --orchestrator orch.example.com:9090 --host web1 --token <token>   # token enrollment
shuttle agent --orchestrator orch.example.com:9090 --host web1 \
  --cert agent.crt --key agent.key --ca ca.crt          # mTLS (omit for insecure dev)

# Synology DSM target
shuttle agent --driver synology --orchestrator … --host nas1
```

## HTTP control plane

| Method & path            | Auth   | Purpose                                  |
|--------------------------|--------|------------------------------------------|
| `GET  /healthz`          | none   | Liveness probe                           |
| `GET  /deploys`          | bearer | List deploy ledger records               |
| `POST /deploy/{service}?sha=…` | bearer | Manually deploy a service at a commit |
| `POST /rollback?service=…&steps=N` | bearer | Roll a service back N deploys |
| `GET  /hosts`            | bearer | List enrollable hosts (token auth)       |
| `POST /enroll`           | bearer | Mint an agent enrollment token           |
| `POST /webhook`          | HMAC   | Git push trigger (signed, replay-guarded)|
| `POST /prune`            | bearer | Delete kept volumes of removed services  |

Bearer auth uses the static `bearer_token` from config. Webhooks verify an
`X-Hub-Signature-256` HMAC and reject replays. Agents authenticate with either a
client cert (mTLS) or a host-scoped enrollment token over server TLS — see
[docs/operations.md](docs/operations.md#enrolling-agents-with-tokens). Full API
reference: [docs/http-api.md](docs/http-api.md).

## IaC repository layout

```
hosts.yaml                            # hosts + labels
services/<name>/<name>.yaml           # service def: host, domains, env, port
services/<name>/docker-compose.yml    # local compose  (XOR with a remote pointer)
```

See [docs/iac-repo.md](docs/iac-repo.md) for the schema, and `examples/repo/`
for a working sample.

## Documentation

- [Architecture & design decisions](docs/architecture.md)
- [Configuration reference](docs/configuration.md)
- [IaC repository schema](docs/iac-repo.md)
- [HTTP API reference](docs/http-api.md)
- [Operations: deploying, mTLS, Synology, releases](docs/operations.md)
- [Contributor guide / repo map (CLAUDE.md)](CLAUDE.md)

## License

See repository.
