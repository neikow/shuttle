# What is Shuttle?

Shuttle is a self-hosted, git-driven Infrastructure-as-Code deployment platform.
It ships as a single Go binary that watches an IaC git repository and rolls
changes out to your own hosts over Docker Compose — with an append-only deploy
ledger, one-command rollback, drift detection, secrets injection, and automatic
Caddy ingress.

Think Portainer's spirit (own your infra, no SaaS), plus full rollback history,
secret management, and multi-host fan-out.

::: tip Status
**v0.1.0** — first release. The core pipeline works end-to-end.
:::

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
  against the deploy ledger, renders each service's compose + env (with
  secrets), and dispatches deploy commands to agents. It also pushes ingress
  routes to Caddy.
- **Agents** are dumb executors. They dial *out* to the orchestrator (no inbound
  firewall holes), receive rendered compose files, and shell out to
  `docker compose`. They report container state back so the orchestrator can
  detect and heal drift.

See the [Architecture](/architecture) page for the full design and the rationale
behind each decision.

## A single binary, two subcommands

One artifact to ship and version. The orchestrator/agent split is a runtime
flag, not a separate build:

```sh
shuttle orchestrator --config /etc/shuttle/config.yml
shuttle agent --orchestrator orch.example.com:9090 --host web1 --token <token>
```

## Next steps

- [Quickstart](/guide/quickstart) — bring up a local dev cluster in one command.
- [Configuration reference](/configuration) — every `config.yml` key.
- [IaC repository schema](/iac-repo) — how to lay out your deploy repo.
- [Operations](/operations) — running on real hosts, mTLS, enrollment, releases.
