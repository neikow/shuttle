# IaC repository schema

The orchestrator deploys whatever this repository declares. A working sample
lives in [`examples/repo/`](https://github.com/neikow/shuttle/tree/main/examples/repo).

## Layout

```
hosts.yaml                            # the hosts Shuttle may deploy to
orchestrator.yaml                     # repo-managed orchestrator overrides (optional)
services/
  <name>/
    <name>.yaml                       # service definition (file name matches dir)
    docker-compose.yml                # compose source — XOR with a remote pointer
```

Parsing is strict (unknown keys rejected) and validated on every sync.

## `orchestrator.yaml`

Optional file in the repo root that overrides selected `config.yml` settings on
each reconcile — no orchestrator restart needed. See
[configuration.md](configuration.md#repo-managed-config-orchestratoryaml) for
the full key reference and override semantics.

```yaml
# All keys are optional.
caddy_admin_url: "http://caddy:2019"
https_redirect: false
secrets_base_path: "/shared"
secrets_path_template: "/services/{service}"
git_credentials:
  - repo_prefix: github.com/myorg
    infisical_key: GITHUB_TOKEN
```

A parse error is logged and old values are kept — a bad commit never blocks
deploys. `shuttle init` writes a commented starter file.

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
env_from: production      # optional; Infisical environment to read secrets from
secret_path: /services/whoami  # optional; Infisical folder for this service (absolute)
env_schema:               # optional; the only secret keys passed to this service
  - WHOAMI_NAME
port: 80                  # optional; the Caddy upstream port for this service's domains
update_policy: rolling    # optional; "rolling" (default) or "recreate"
delete_volumes: manual    # optional; volume deletion policy on removal (default: manual)
caddy_snippet: |          # optional; JSON array of Caddy HTTP handlers
  [{"handler":"headers","response":{"set":{"X-Frame-Options":["DENY"]}}}]
```

### Field notes

- **`host`** must reference a declared host, or the sync fails.
- **`domains` + `port`** — a service with both gets a Caddy route: each domain
  proxies to `<host>:<port>`. A service missing either is not routed.
- **`env_from`** selects the Infisical environment (e.g. `production`,
  `staging`) the service's secrets are read from, overriding the orchestrator's
  default `INFISICAL_ENV`. Omit it to use that default.
- **`secret_path`** is the Infisical folder this service's secrets are read from,
  overriding the orchestrator's `secrets_path_template`. Must be absolute. The
  orchestrator always also reads the shared `secrets_base_path` and merges it
  under the service folder (service keys win). See
  [configuration.md](configuration.md#secrets-providers).
- **`env_schema`** scopes secrets: the orchestrator passes the service only these
  keys (filtered from the configured secrets provider). Without it, no secrets
  flow to the service.
- **`update_policy`** controls how the agent applies a new deploy:
  - `rolling` (default) — zero-downtime: brings up new containers alongside the
    old, waits until they are healthy, then removes the old. Requires the service
    to run two-up (no fixed host port, no `container_name`). `shuttle check` warns
    if these constraints are violated.
  - `recreate` — stop-then-start (compose default). Simpler but causes a brief
    outage. Always used for rollbacks.
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
