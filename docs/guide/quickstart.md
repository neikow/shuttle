# Quickstart — running in 3 minutes

Install the `shuttle` binary, then run an orchestrator and an agent on this
machine and deploy a real service to it. No cloud account, no cluster — just the
binary, Docker, and git.

## What you need

- The **`shuttle` binary** — [install it](/guide/installation) (one command):
  ```sh
  VERSION=0.1.0   # see https://github.com/neikow/shuttle/releases/latest
  curl -sSL "https://github.com/neikow/shuttle/releases/download/v${VERSION}/shuttle_${VERSION}_$(uname -s | tr A-Z a-z)_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz" \
    | tar -xz shuttle && sudo install shuttle /usr/local/bin/
  ```
- **Docker** with Compose v2 (`docker compose version`)
- **git** and **curl**

## 1. Create a tiny IaC repo and config

Shuttle deploys whatever a git repo declares. Make a minimal one — one host, one
service:

```sh
mkdir -p shuttle-demo/iac/services/whoami && cd shuttle-demo

cat > iac/hosts.yaml <<'YAML'
hosts:
  - name: local
YAML

cat > iac/services/whoami/whoami.yaml <<'YAML'
name: whoami
host: local
update_policy: recreate   # lets the demo publish a fixed port
YAML

cat > iac/services/whoami/docker-compose.yml <<'YAML'
services:
  whoami:
    image: traefik/whoami:latest
    ports: ["8088:80"]
    restart: unless-stopped
YAML

( cd iac && git init -q -b main && git add -A \
  && git -c user.email=demo@shuttle.local -c user.name=demo commit -qm "seed" )

cat > config.yml <<YAML
bearer_token: "demo-token"
http_addr: ":8080"
grpc_addr: ":9090"
data_dir: ./data
repo_url: "file://$(pwd)/iac"
repo_branch: "main"
webhook_secret: "demo-secret"   # repo_url + webhook_secret enable the git-sync loop
YAML
```

## 2. Start the orchestrator

```sh
shuttle orchestrator --config config.yml
```

It serves the control plane on `:8080` and waits for agents on `:9090`. Leave it
running.

## 3. Start an agent (new terminal)

From the same `shuttle-demo` directory, point an agent at the orchestrator as host
`local`. With no TLS configured the link is insecure — fine for a local demo:

```sh
shuttle agent --orchestrator localhost:9090 --host local
```

The agent dials out, registers as `local`, and is ready to deploy.

## 4. Watch your instance come up

Within ~60s the orchestrator reconciles the repo and deploys `whoami` to the
agent. Then:

```sh
curl localhost:8088
# Hostname: ... / served by the whoami container

curl -s -H "Authorization: Bearer demo-token" localhost:8080/deploys | jq
# the deploy recorded in the append-only ledger
```

You now have an orchestrator and a running service instance, deployed from git. 🎉

## 5. Change something and redeploy

It's just git — edit, commit, and the reconciler rolls it out:

```sh
cd iac
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

1. You committed to the **IaC repo** → the orchestrator saw a new commit.
2. It diffed desired (repo) vs actual (ledger), **rendered** the compose file, and
   dispatched it down the gRPC stream to the agent.
3. The **agent** ran `docker compose up` and reported the result.
4. The orchestrator recorded it in the **ledger** — which powers one-click
   rollback.

In production the agent runs on a *separate* server (dialing out, no inbound
ports) and you push to a remote git repo. That's the next guide:

- [Installation](/guide/installation) — install on each host.
- [Deploy to a real host](/guide/first-deployment) — orchestrator + your own
  server, with enrollment and HTTPS.

::: tip Contributors
Hacking on Shuttle itself? The repo ships a one-command multi-host playground —
`make dev-up` builds from source and brings up the orchestrator, the web UI, and
two simulated remote hosts. See the README.
:::
