---
layout: home

hero:
  name: Shuttle
  text: Git-driven deploys to your own hosts
  tagline: >-
    Self-hosted Infrastructure-as-Code in a single Go binary. Watch an IaC repo,
    roll Docker Compose changes out over gRPC, and keep an append-only ledger
    that powers one-command rollback and drift detection.
  actions:
    - theme: brand
      text: Get running in 3 minutes
      link: /guide/quickstart
    - theme: alt
      text: What is Shuttle?
      link: /guide/getting-started
    - theme: alt
      text: View on GitHub
      link: https://github.com/neikow/shuttle

features:
  - icon: 📓
    title: Append-only deploy ledger
    details: >-
      Every deploy is recorded in a single-file SQLite ledger (WAL, pure-Go, no
      CGO). Rollback is "redeploy an older recorded SHA" — not mutating state.
  - icon: 🔁
    title: Zero-downtime rolling deploys
    details: >-
      The default for every service: bring new containers up alongside the old,
      health-gate them behind Caddy, then cull the old. A failed deploy never
      causes downtime.
  - icon: 🔌
    title: Agents dial out
    details: >-
      Agents open the gRPC stream to the orchestrator, so managed hosts need no
      inbound firewall holes. Commands flow down the same stream heartbeats and
      container state flow up.
  - icon: 🧠
    title: Orchestrator renders, agents are dumb
    details: >-
      All git, diffing, secret resolution and templating happen on the
      orchestrator. Agents receive a finished compose file + env and just run
      it — trivial and secret-free at rest.
  - icon: 🔐
    title: Secrets & RBAC built in
    details: >-
      Infisical or file-based secret providers, per-user OIDC, named role-scoped
      control tokens (read < deploy < admin), and an append-only audit log of
      every control-plane mutation.
  - icon: 🌐
    title: Automatic Caddy ingress
    details: >-
      Routes are derived from each service's domains + port and pushed to a
      per-host Caddy instance with automatic Let's Encrypt — declarative, no
      drift.
  - icon: 🩺
    title: Drift detection & self-heal
    details: >-
      Agents report container state every ~30s. The reconciler heals SHA drift
      and crashed or missing containers, so reality is pulled back to the repo.
  - icon: 📊
    title: Observability & notifications
    details: >-
      An in-process event bus feeds Prometheus metrics, an SSE event stream, and
      outbound Slack / Discord / webhook notifications — plus an embedded React
      dashboard.
---
