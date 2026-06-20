# Quickstart — running in 3 minutes

Install the `shuttle` binary, then let `shuttle init` scaffold a **secure**
orchestrator and an example service on this machine. No cloud account, no
cluster — just the binary, Docker, and git.

## What you need

- The **`shuttle` binary** — [install it](/guide/installation) (one command,
  verifies the checksum + signature):
  ```sh
  curl -sSfL https://neikow.github.io/shuttle/install | bash
  ```
- **Docker** with Compose v2 (`docker compose version`)
- **git** and **curl**

## 1. Run the setup wizard

`shuttle init` is the blessed bootstrap path. In an empty directory, run it and
**press Enter through every prompt** — the defaults give you a secure setup:

```sh
mkdir shuttle-demo && cd shuttle-demo
shuttle init
```

Accepting the defaults gets you:

- **Token enrollment over TLS** — the agent link is encrypted and authenticated.
  A self-signed orchestrator cert is generated for you under `./certs`, so
  there's no `openssl`, no CA to copy, and no per-agent certificates.
- **Auto-generated secrets** — the control-plane bearer token and webhook secret
  are random and written to `config.yml` at mode `0600`.
- **A starter IaC repo** at `./iac-repo` with one host (`local`) and one
  runnable service (`whoami`). With no remote, the orchestrator drives this
  local repo directly (`repo_url: file://…`), so nothing to push.

When it finishes it prints the exact next commands. They're reproduced below.

## 2. Start the orchestrator

```sh
shuttle orchestrator --config config.yml
```

It serves the control plane on `:8080` and waits for agents on `:9090` over TLS.
Leave it running.

## 3. Enroll and start the agent (new terminal)

Enrollment is SSH-like: mint a single-use join token, then run the printed
one-liner on the host. From the same `shuttle-demo` directory:

```sh
shuttle enroll --config config.yml --host local
```

It prints a ready-to-run command — copy and run it (a new terminal is fine for
this local demo):

```sh
shuttle agent join --redeem-url http://localhost:8080 --token <join-token>
```

`join` redeems the token for a long-lived, host-scoped agent credential, stores
it, and starts the agent as host `local`. The powerful bearer token never leaves
the orchestrator — only the scoped, expiring join token is carried to the host.

::: tip Reaching a real host over HTTPS
On localhost the control plane is plain HTTP, so enroll warns it can't pin the
cert. When the orchestrator is reachable over HTTPS, enroll embeds a `--pin` of
its certificate (trust-on-first-use) so the host needs no CA file. See
[Deploy to a real host](/guide/first-deployment).
:::

## 4. Watch your instance come up

Within ~60s the orchestrator reconciles the repo and deploys `whoami` to the
agent. Then:

```sh
curl localhost:8088
# Hostname: ... / served by the whoami container

curl -s -H "Authorization: Bearer $(grep bearer_token config.yml | cut -d'"' -f2)" \
  localhost:8080/deploys | jq
# the deploy recorded in the append-only ledger
```

You now have a **secured** orchestrator and a running service instance, deployed
from git. 🎉

## 5. Change something and redeploy

It's just git — edit, commit, and the reconciler rolls it out:

```sh
cd iac-repo
sed -i'' -e 's#traefik/whoami:latest#traefik/whoami:v1.10#' services/whoami/docker-compose.yml
git commit -am "pin whoami version"
```

A new deploy appears in `/deploys`; the previous one is one
[rollback](/http-api#post-rollback) away.

## 6. Tear it down

Stop the orchestrator and agent (Ctrl-C in each terminal), then:

```sh
docker rm -f $(docker ps -aqf name=whoami) shuttle-caddy 2>/dev/null
docker network rm shuttle 2>/dev/null
cd .. && rm -rf shuttle-demo
```

## What just happened

This is the full Shuttle pipeline:

1. `shuttle init` scaffolded a secure orchestrator (TLS + token enrollment) and
   an **IaC repo** the orchestrator watches.
2. The agent **enrolled** over TLS with a single-use token — no inbound ports, no
   cert distribution.
3. The orchestrator diffed desired (repo) vs actual (ledger), **rendered** the
   compose file, and dispatched it down the gRPC stream to the agent.
4. The **agent** ran `docker compose up` and reported the result; the
   orchestrator recorded it in the **ledger** — which powers one-click rollback.

In production the agent runs on a *separate* server (dialing out, no inbound
ports) and you push to a remote git repo. That's the next guide:

- [Installation](/guide/installation) — install on each host.
- [Deploy to a real host](/guide/first-deployment) — orchestrator + your own
  server, with enrollment and HTTPS.

::: tip Want a different setup?
`shuttle init` also offers mutual TLS or an insecure local link, an empty repo
scaffold or your own remote, Infisical secrets, Caddy ingress, and GitHub
Actions. Pick non-default answers at the prompts.
:::

::: tip Contributors
Hacking on Shuttle itself? The repo ships a one-command multi-host playground —
`make dev-up` builds from source and brings up the orchestrator, the web UI, and
two simulated remote hosts. See the README.
:::
