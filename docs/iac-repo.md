# IaC repository schema

The orchestrator deploys whatever this repository declares. A working sample
lives in [`examples/repo/`](../examples/repo).

## Layout

```
hosts.yaml                            # the hosts Shuttle may deploy to
services/
  <name>/
    <name>.yaml                       # service definition (file name matches dir)
    docker-compose.yml                # compose source — XOR with a remote pointer
```

Parsing is strict (unknown keys rejected) and validated on every sync.

## `hosts.yaml`

```yaml
hosts:
  - name: web1            # must match the agent's --host
    labels:
      region: eu-west
      role: edge
```

- `name` is required and must be unique.
- `labels` are free-form metadata.

## `services/<name>/<name>.yaml`

```yaml
name: whoami              # must equal the directory name
host: web1                # must reference a host in hosts.yaml
domains:                  # optional; drive Caddy ingress
  - whoami.example.com
env_from: production      # optional; secrets environment/scope
env_schema:               # optional; the only secret keys passed to this service
  - WHOAMI_NAME
port: 80                  # optional; the Caddy upstream port for this service's domains
delete_volumes: manual    # optional; volume deletion policy on removal (default: manual)
caddy_snippet: |          # optional; JSON array of Caddy HTTP handlers
  [{"handler":"headers","response":{"set":{"X-Frame-Options":["DENY"]}}}]
```

### Field notes

- **`host`** must reference a declared host, or the sync fails.
- **`domains` + `port`** — a service with both gets a Caddy route: each domain
  proxies to `<host>:<port>`. A service missing either is not routed.
- **`env_schema`** scopes secrets: the orchestrator passes the service only these
  keys (filtered from the configured secrets provider). Without it, no secrets
  flow to the service.
- **`caddy_snippet`** must be a JSON array of Caddy HTTP handler objects. They are
  injected *ahead of* the `reverse_proxy` handler for the service's domains
  (headers, rewrites, auth, …). Invalid JSON is a hard error.
- **`delete_volumes`** controls what happens to the service's named volumes when
  it is removed from the repo. When a service is deleted, the orchestrator always
  brings its containers down; this setting decides the *volumes*:
  - `manual` (default) — keep the volumes; delete them only with `shuttle prune`
    (or `POST /prune`). Safest: no data loss on an accidental removal.
  - `true` / `immediate` — delete the volumes as soon as the service is removed.
  - `false` — same as `manual`.
  - a duration like `"7 days"`, `"30 minutes"`, `"12h"` — keep the volumes, then
    delete them once that long has passed since removal.

  Accepts a YAML boolean or a quoted string. The policy in effect is the one
  recorded the last time the service was present in the repo.

## Compose source — exactly one

Each service must have **exactly one** compose source (XOR), or the sync fails:

### Local compose

A `docker-compose.yml` next to the service definition:

```
services/whoami/docker-compose.yml
```

### Remote pointer

A `remote:` block in the service yaml instead of a local file:

```yaml
remote:
  repo: https://github.com/you/app.git
  branch: main
  path: deploy/docker-compose.yml
```

The orchestrator shallow-clones the pointer repo into a cache and reads the file
at `path`. Declaring both a remote pointer and a local `docker-compose.yml` is a
validation error ("XOR violation"); declaring neither is also an error.

## How it maps to a deploy

On each reconcile the orchestrator loads this repo, diffs every service's source
SHA against the ledger, and for changed services renders the compose + env and
dispatches a deploy to the service's `host`. The current commit SHA is what the
ledger records and what rollback targets.
