# Changelog

All notable changes to Shuttle are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Cloudflare and Route53 DNS-challenge providers.** `dns.yml` providers now
  accept `type: cloudflare` (`api_token`) and `type: route53` (`access_key_id`,
  `secret_access_key`, `region`) alongside `ovh`. The shipped
  `ghcr.io/neikow/shuttle-caddy` image bundles all three plugins; the editor and
  `shuttle scaffold dns-provider` prefill each type's credential keys.

### Changed

- **Service env is now an explicit `env:` map, replacing `env_schema:`.**
  ⚠️ **Breaking.** A service declares each variable it wants and *where it comes
  from*, instead of a flat list of provider keys:
  - `env: { FOO: "" }` — read `FOO` from the configured secrets provider (the old
    `env_schema: [FOO]` behaviour).
  - `env: { FOO: "${infisical:BAR}" }` / `${secret:BAR}` / `${BAR}` — read
    provider key `BAR` (so a provider secret can ship under a different name).
  - `env: { FOO: "${env:BAR}" }` — read `BAR` from the **orchestrator's** own
    process environment.
  - `env: { FOO: "literal" }` — a literal value; tokens may be embedded
    (`https://${env:REGION}.example.com/${secret:PATH}`).

  Migrate `env_schema: [A, B]` to `env: { A: "", B: "" }`.

### Fixed

- **A service that uses no secrets no longer requires an (empty) provider entry.**
  Env values are resolved only when referenced, so a service with no `env:` — or
  one using only literals / `${env:}` — performs **no provider lookup** and needs
  no Infisical folder to exist. Previously an empty `env_schema` fetched and
  passed through the whole folder, forcing every service to have one. An
  unresolved reference is still a hard error (and is reported by `shuttle check`).

## [0.4.0] - 2026-06-21

An ingress, data, and tooling release. v0.4.0 puts wildcard TLS and a reverse
proxy in front of anything, gives stateful services first-class backups, and adds
an editor language server so the IaC files write themselves. Backward
compatible — existing repos and configs are unchanged; every new capability is
opt-in.

> Shuttle is **alpha**: the CLI, config, and HTTP API may still change between
> releases without a deprecation path. Pin a version for anything you rely on.

### Ingress & TLS

- **Wildcard certificates via a DNS-01 challenge.** A new optional `dns.yml` in
  the IaC repo declares DNS **providers** (OVH to start) and **certificates**
  (incl. `*.example.com`). The orchestrator issues each over an ACME DNS-01
  challenge — the only challenge that mints wildcards and that needs no inbound
  `:80`/`:443` — so one cert serves every subdomain (one ACME order, not N). A
  service's domain is auto-matched to a covering certificate; an optional
  `tls_certificate:` pins one explicitly. Domains covered by no certificate keep
  the existing per-domain Let's Encrypt (HTTP-01). Provider credentials are
  resolved from the secrets provider and never committed. Ships a
  `ghcr.io/neikow/shuttle-caddy` image (stock Caddy + the OVH DNS plugin); the
  agent defaults its sidecar to it, overridable with `--caddy-image`.
- **External (proxy-only) services.** A service may declare an `external:
  {upstream}` block instead of a compose source: Shuttle **routes to it but never
  deploys it** — HTTPS + reverse proxy (incl. `dns.yml` wildcards, `caddy_snippet`,
  `tls_certificate`) in front of infrastructure you run out-of-band. The canonical
  case is an Infisical instance that Shuttle's own secrets provider depends on.
- **Choose the Caddy sidecar ports per host.** A host's `caddy: {http_port,
  https_port}` in `hosts.yaml` relocates its ingress ports (for a box already
  using `:80`/`:443`, or one behind a port-forwarding load balancer).

### Backups

- **Service-data backups.** Distinct from the ledger snapshot (`shuttle backup`),
  Shuttle now backs up a service's *persistent data*. Two engines — `volume` (tar
  the project's named volumes) and `postgres` (`pg_dump`/`pg_dumpall`) — to two
  stores — `local` or **restic** (dedup, encryption, local-or-remote, retention via
  `restic forget`). Per-service `backup:` policy (engine/schedule/retention) lives
  in the repo; backend credentials come from the secrets provider via
  `config.yml` `backups:`. A scheduler runs due backups, and `before_deploy` takes
  a snapshot before each deploy. Restore is a separate, admin-tier, explicitly
  confirmed action (stop → restore → start), decoupled from rollback. Triggered,
  listed, and restored via `shuttle backup-service` / `backups` / `restore-service`
  and the `POST /backup/{service}` / `GET /backups` / `POST /restore` endpoints.

### Editor tooling

- **A language server for the IaC files.** `shuttle lsp` is an LSP server (a
  subcommand of the one binary) providing completion and live validation for
  `hosts.yaml`, services, `dns.yml`, and `orchestrator.yaml`. It reuses Shuttle's
  own config loader, so the editor never drifts from what the orchestrator
  accepts: diagnostics flag unknown keys / type / syntax errors as you type, and
  completion offers schema field names, enum values, and cross-file references
  (`host:` → `hosts.yaml`, `tls_certificate:` → `dns.yml`, …). A thin **VS Code
  extension** (`editors/vscode/`) launches it; any LSP-capable editor can use it
  too.

## [0.3.0] - 2026-06-20

An onboarding release. v0.3.0 makes the first five minutes secure *and* easy:
`shuttle init` now defaults to an encrypted, authenticated setup and generates
the TLS material itself, and a single verified command installs the binary.
Backward compatible — existing configs, and the wizard's advanced paths, are
unchanged.

> Shuttle is **alpha**: the CLI, config, and HTTP API may still change between
> releases without a deprecation path. Pin a version for anything you rely on.

### Onboarding

- **`shuttle init` is secure by default.** Pressing Enter through the wizard now
  yields token enrollment over TLS — not an insecure demo. It **generates the
  orchestrator's self-signed cert inline**, so there's no `openssl` / `make certs`
  step and no CA to distribute: agents trust-on-first-use pin the cert and
  receive it at enrollment.
- **Pick your starting repo.** The wizard scaffolds a runnable **starter** repo
  (a `whoami` service + a `local` host), an **empty** scaffold, or points at an
  **existing** remote. A local starter with no remote self-drives via a `file://`
  `repo_url`, so the very first deploy works with nothing to push.
- **`config.yml` gains `advertise_control_url`**, so `shuttle enroll --config`
  needs no `--url`.

### Installation

- **Verified one-line install:**
  `curl -sSfL https://neikow.github.io/shuttle/install | bash`. It detects
  OS/arch, always verifies the published SHA-256 checksum, and verifies the
  keyless cosign signature when `cosign` is installed. Configurable via
  `SHUTTLE_VERSION`, `SHUTTLE_INSTALL_DIR`, `SHUTTLE_OS` / `SHUTTLE_ARCH`, and
  `SHUTTLE_NO_VERIFY`.

### Fixes

- A comment-only `orchestrator.yaml` (what `init` scaffolds) is now treated as
  present-but-empty instead of logging an `orchestrator.yaml invalid` warning on
  every reconcile.

### Documentation

- README rewritten around the project description, end-user install, and an easy
  contributor onboarding; the guides (quickstart, first deployment, installation,
  operations) now run through `shuttle init`.
- **Alpha disclaimer** added across the README and docs.

## [0.2.0] - 2026-06-20

A large maturity release. v0.1.0 proved the pipeline end to end; v0.2.0 makes it
operable, secure, and observable for real-world use — with a web dashboard,
per-user auth, an audit trail, secrets, zero-downtime deploys, and a signed
supply chain. Upgrading is drop-in: the ledger self-migrates on first start, and
every addition is backward compatible (the static `bearer_token` still works).

### Security & access control

- **RBAC** — named, role-scoped control-plane tokens (`read < deploy < admin`),
  SHA-256 hashed at rest, managed by `shuttle token create/list/revoke`. The
  static `bearer_token` remains the bootstrap admin.
- **OIDC per-user auth** — accept an OpenID Connect JWT as a bearer, mapped to a
  role via a configurable claim; the subject becomes the audit actor.
- **OIDC login in the web UI** — "Sign in with SSO" runs an Authorization Code +
  PKCE flow in the browser and uses the resulting ID token as its bearer.
- **Audit log** — append-only record of every control-plane mutation (who, what,
  target, source IP, result), exposed at `GET /audit` and `shuttle audit`.
- **Hardening** — per-IP rate limiting on unauthenticated endpoints, baseline
  security headers + a UI Content-Security-Policy, constant-time bearer
  comparison, HTTP server timeouts (Slowloris), opt-in `/metrics` auth, and git
  tokens kept out of the clone URL / process args.

### Web UI

- Embedded React dashboard (build-tag `embedui`): hosts + service-health
  Overview, deploy history, live event stream, plan/check, hosts.
- Role-gated **mutations**: redeploy, rollback, prune, token CRUD, repo-webhook
  CRUD, and agent enrollment — shown by the caller's role.
- **Per-deploy logs** — the captured output of each deploy/rollback is persisted
  and viewable per row.

### Deploys & reliability

- **Zero-downtime rolling deploys** by default — new containers come up and pass
  health checks before the old are removed; a failed deploy never causes
  downtime. `recreate` available per service.
- **Service teardown** when a service is removed from the repo, with a
  `delete_volumes` policy (`manual` default, `immediate`, or a duration) and
  `shuttle prune`.
- **Drift heal** reconciles the agent's deployed set from disk on restart.
- **Ledger backup/restore** (`shuttle backup` / `restore`) via SQLite
  `VACUUM INTO`.
- `/readyz` readiness probe + graceful drain on shutdown; collision-proof deploy
  IDs; bounded gRPC stop so Ctrl-C exits.

### Secrets

- **File (dotenv) secrets provider** — no external dependency; mirrors the
  Infisical folder layout.
- Infisical: base + per-service folder structure, `env_from` selects the
  environment, secret-change **webhook** (selective, debounced redeploy) and
  **polling** fallback (fingerprints only — values never stored).
- A key declared in `env_schema` but missing from the resolved secrets is now a
  **hard error** instead of a silent empty value.
- Per-repo HTTPS **git credentials** from Infisical, injected at call time.

### Observability & notifications

- In-process **event bus** feeding: **Prometheus metrics** (`/metrics`), an
  **SSE event stream** (`/events`, `shuttle events`), and **outbound
  notifications** to Slack / Discord / generic webhooks.
- Agent **version skew** surfaced per host and warned on connect.

### Ingress

- Explicit service `port` + `https_redirect` setting; the agent's Caddy ingress
  sidecar is now always-on (the `--caddy` flag is gone).

### Tooling & CLI

- `shuttle init` bootstrap wizard + repo-managed `orchestrator.yaml`.
- `shuttle plan` and `shuttle check` (read-only diff / validation), dual local +
  remote, ref-aware — plus a CI **plan-comment** GitHub Action.
- **SSH-like enrollment**: `shuttle enroll` mints a single-use join token and
  prints a cert-pinned `shuttle agent join` one-liner (TOFU; no CA file to copy).
- Per-service **repo webhook** trigger endpoint + CLI.
- One-command **dev cluster** (`make dev-up`) with simulated remote hosts.
- Loads `CWD/.env` at startup; `--debug` flag.

### Supply chain

- Releases now ship **keyless cosign signatures** and a **per-archive SBOM**
  (syft), in addition to multi-arch `ghcr.io/neikow/shuttle` images and
  checksums. (First release to carry them.)

### Documentation

- **Hosted documentation site** (VitePress → GitHub Pages):
  <https://neikow.github.io/shuttle/>, with a user-centered getting-started,
  3-minute quickstart, installation, and first-real-deployment guides.

### Internal

- Replaced the `goose` migration dependency with a small embedded-SQL migrator.

## [0.1.0] - 2026-05-22

First release. The core pipeline works end to end: a signed webhook triggers a
git sync + reconcile, the orchestrator renders each service's compose + env and
dispatches it over gRPC to an agent, which runs `docker compose up` and reports
back to an append-only SQLite ledger that powers rollback and drift detection.
Includes mTLS, token enrollment, Caddy ingress, the Synology driver, and
GoReleaser-published archives + images.

[0.4.0]: https://github.com/neikow/shuttle/releases/tag/v0.4.0
[0.3.0]: https://github.com/neikow/shuttle/releases/tag/v0.3.0
[0.2.0]: https://github.com/neikow/shuttle/releases/tag/v0.2.0
[0.1.0]: https://github.com/neikow/shuttle/releases/tag/v0.1.0
