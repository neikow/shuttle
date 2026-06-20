# What is Shuttle?

Shuttle deploys your apps to **your own servers** from a **git repository** — no
SaaS, no control-plane bill, no Kubernetes. Push a commit, and your services roll
out over Docker Compose with zero-downtime, a full deploy history, and one-click
rollback.

Think of it as your own tiny Heroku/Vercel that runs on hardware you control.

::: tip Want to see it first?
[**Get a full environment running in 3 minutes →**](/guide/quickstart)
:::

## Why Shuttle

- **You own everything.** Your servers, your data. The deploy history lives in a
  single SQLite file you can back up with one command.
- **Git is the source of truth.** Your infrastructure is a repo. A deploy is a
  commit; a rollback is redeploying an older one.
- **Zero-downtime by default.** New containers come up and pass health checks
  *before* the old ones are removed — a bad deploy never takes you offline.
- **Self-healing.** Agents report what's actually running; the orchestrator pulls
  reality back to what the repo declares.
- **No inbound holes.** Agents dial *out* to the orchestrator, so your managed
  hosts need no open ports.
- **One binary.** Everything ships as a single Go binary — `shuttle orchestrator`
  on your control host, `shuttle agent` on each server.

## Is it for me?

Shuttle fits if you run a handful of services on a few VMs or a home lab and want
git-driven deploys without operating Kubernetes or paying a PaaS. It's **not** a
container scheduler — it deploys declared services to declared hosts, predictably.

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

- The **orchestrator** is the brain. It watches your IaC repo, figures out what
  changed, renders each service's compose file + secrets, and tells agents what to
  run. It also configures Caddy for HTTPS ingress.
- **Agents** are dumb executors on each host. They dial out to the orchestrator,
  receive a finished compose file, and run it — then report back what's running so
  drift can be healed.

Want the full design and the reasoning behind each choice? See the
[Architecture](/architecture) reference.

## Next steps

1. [**Quickstart**](/guide/quickstart) — a complete environment in 3 minutes.
2. [**Installation**](/guide/installation) — install the binary or container image.
3. [**Deploy to a real host**](/guide/first-deployment) — orchestrator + your own
   server, deploying your first service.
