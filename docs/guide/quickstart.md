# Quickstart

## Local dev stack

Bring up orchestrator + agent + Caddy with Docker Compose:

```sh
docker compose -f deploy/docker-compose.yml up --build
```

- HTTP control plane: `:8080`
- gRPC (agents dial in): `:9090`
- Caddy admin API: `:2019`

The agent mounts the Docker socket so it can run compose deploys, and dials the
orchestrator over an **insecure** gRPC channel (dev only). For a mutual-TLS link
between orchestrator and agent:

```sh
make certs
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.mtls.yml up --build
```

See [Operations](/operations#local-dev-stack) for details.

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
# Bootstrap a new environment (interactive wizard)
shuttle init

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

## Web UI

Build the binary with the embedded dashboard:

```sh
make build-ui   # runs make web, then embeds web/dist (-tags embedui)
```

The UI is served under `/ui/` (unauthenticated static bundle; the browser
authenticates its own API calls with the bearer token you paste into the UI).
During development, `make web-dev` starts a Vite dev server that proxies API
requests to a local orchestrator on `:8080`.

## IaC repository layout

```
hosts.yaml                            # hosts + labels
orchestrator.yaml                     # repo-managed orchestrator overrides (optional)
services/<name>/<name>.yaml           # service def: host, domains, env, port
services/<name>/docker-compose.yml    # local compose  (XOR with a remote pointer)
```

`orchestrator.yaml` lets you change Caddy settings, secrets paths, and Git
credentials via a git commit — no orchestrator restart needed. See the
[IaC repository schema](/iac-repo) for the full layout and
[Configuration](/configuration#repo-managed-config-orchestratoryaml) for the
config split. A working sample lives in
[`examples/repo/`](https://github.com/neikow/shuttle/tree/main/examples/repo).
