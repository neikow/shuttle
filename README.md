# Shuttle

**Git-driven deployments to your own servers — no SaaS, no Kubernetes.**

Shuttle is a self-hosted deployment platform shipped as a single Go binary. It
watches an Infrastructure-as-Code git repository and rolls changes out to your
hosts over Docker Compose, recording every deploy in an append-only SQLite
ledger that powers one-command rollback and drift detection.

Think of it as your own tiny Heroku/Vercel that runs on hardware you control.

> **Status: v0.4.0** — wildcard DNS-01 TLS, external (proxy-only) services,
> service-data backups, an IaC language server, secure one-command onboarding,
> web dashboard, RBAC + OIDC, audit log, zero-downtime deploys, observability, and
> a signed supply chain. See the [changelog](CHANGELOG.md).
>
> ⚠️ **Alpha software.** It's tested and usable, but the CLI, config, and HTTP
> API may change between releases without a deprecation path. Pin a version for
> anything you rely on.

## Highlights

- **Git is the source of truth.** A deploy is a commit; a rollback is redeploying
  an older one. The full history lives in a single SQLite file you can back up
  with one command.
- **Zero-downtime by default.** New containers come up and pass health checks
  *before* the old ones are removed — a bad deploy never takes you offline.
- **No inbound holes.** Agents dial *out* to the orchestrator, so managed hosts
  need no open ports.
- **Secure onboarding.** `shuttle orchestrator init` sets up TLS + SSH-like token
  enrollment for you — it generates a self-signed cert, so there's no `openssl`
  step and no CA to distribute.
- **Backups built in.** Schedule (or auto-snapshot before each deploy) a service's
  data — Docker volumes or postgres — to restic (dedup + encryption, local or S3)
  or a local dir, and restore it from the control plane.
- **Batteries included.** Secret injection (Infisical or file), per-user OIDC and
  role-scoped tokens, an audit log, Prometheus metrics, Slack/Discord
  notifications, automatic Caddy ingress (incl. DNS-challenge wildcard certs via
  `dns.yml`), and an embedded web dashboard.
- **One binary.** `shuttle orchestrator` on your control host, `shuttle agent` on
  each server — the split is a runtime flag, not a separate build.

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
  and dispatches deploys to agents. It also pushes ingress routes to Caddy.
- **Agents** are dumb executors. They dial out to the orchestrator, receive a
  finished compose file, and shell out to `docker compose` — then report
  container state back so the orchestrator can detect and heal drift.

## Install

The install script detects your platform, downloads the matching release,
verifies its SHA-256 checksum (and the [cosign](https://docs.sigstore.dev/)
signature when `cosign` is present), then installs the binary:

```sh
curl -sSfL https://neikow.github.io/shuttle/install | bash
```

Configurable via env vars — `SHUTTLE_VERSION`, `SHUTTLE_INSTALL_DIR`,
`SHUTTLE_OS`/`SHUTTLE_ARCH`:

```sh
curl -sSfL https://neikow.github.io/shuttle/install | SHUTTLE_VERSION=0.4.0 bash
```

Releases ship an SBOM and keyless cosign signatures. Other methods — container
image, `go install`, building from source, manual download — are in the
[Installation guide](https://neikow.github.io/shuttle/guide/installation).

## Get running

Two short commands scaffold a **secure** setup. Run them in an empty directory
and press Enter through the prompts:

```sh
mkdir shuttle-demo && cd shuttle-demo
shuttle init               # scaffold the IaC git repo
shuttle orchestrator init  # generate the server config + TLS
```

`shuttle init` writes a starter IaC repo with a runnable example service.
`shuttle orchestrator init` writes the server config — its defaults give you TLS
with SSH-like token enrollment (a self-signed cert is generated — no `openssl`,
no CA to copy) and auto-generated secrets at mode `0600` — and auto-detects the
repo you just scaffolded. (Pass `--advanced` to either for every option.) Then:

```sh
shuttle orchestrator --config config.yml          # terminal 1
shuttle enroll --config config.yml --host local   # terminal 2 — prints a join command
```

Run the printed `shuttle agent join …` command and your first service deploys.
Full walkthrough: the
[3-minute Quickstart](https://neikow.github.io/shuttle/guide/quickstart). To
deploy to a real server, see
[Deploy to a real host](https://neikow.github.io/shuttle/guide/first-deployment).

The HTTP control plane (deploy, rollback, plan, audit, webhooks, …) is documented
in the [API reference](https://neikow.github.io/shuttle/http-api).

## Web UI

Build the binary with the embedded React dashboard:

```sh
make build-ui   # runs make web, then embeds web/dist (-tags embedui)
```

It's served under `/ui/`. The browser authenticates its own API calls with a
bearer token you paste in, or via **Sign in with SSO** (OIDC) when configured.

## Development

Contributions welcome. You need **Go 1.25+**, **Docker** with Compose v2, and
**git**.

```sh
git clone https://github.com/neikow/shuttle.git
cd shuttle
make build      # -> ./shuttle (version stamped from git)
make test       # unit tests, race-enabled — the default gate
make lint       # golangci-lint v2
```

Spin up a full local cluster — orchestrator + web UI + two **simulated remote
hosts** (each an isolated Docker-in-Docker engine running a self-enrolling agent)
— with one command:

```sh
make dev-up     # UI at http://localhost:8080/ui/  (bearer token: test-bearer)
make dev-logs   # follow logs
make dev-down   # tear it down
```

[**CLAUDE.md**](CLAUDE.md) is the repo map: the architecture, the package layout,
and the rationale behind each design decision. Read it before changing structural
code.

## Documentation

📖 **Full docs: <https://neikow.github.io/shuttle/>** (built from `docs/` with
VitePress; `make docs-dev` to preview locally).

- [Getting started](https://neikow.github.io/shuttle/guide/getting-started)
- [Architecture & design decisions](https://neikow.github.io/shuttle/architecture)
- [Configuration reference](https://neikow.github.io/shuttle/configuration)
- [IaC repository schema](https://neikow.github.io/shuttle/iac-repo)
- [Editor support (language server + VS Code)](https://neikow.github.io/shuttle/editor)
- [HTTP API reference](https://neikow.github.io/shuttle/http-api)
- [Operations: deploying, mTLS, Synology, releases](https://neikow.github.io/shuttle/operations)

## License

See repository.
