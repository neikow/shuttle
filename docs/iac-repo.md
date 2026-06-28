# IaC repository schema

The orchestrator deploys whatever this repository declares. A working sample
lives in [`examples/repo/`](https://github.com/neikow/shuttle/tree/main/examples/repo).

> 💡 Editing these files is much easier with [editor support](editor): `shuttle
> lsp` gives completion and live validation driven by this same schema.

## Layout

```
hosts.yaml                            # the hosts Shuttle may deploy to
orchestrator.yaml                     # repo-managed orchestrator overrides (optional)
dns.yml                               # DNS-challenge cert providers + certificates (optional)
services/
  <name>/
    <name>.yaml                       # service definition (file name matches dir)
    docker-compose.yml                # compose source — XOR with a remote pointer
```

Parsing is strict (unknown keys rejected) and validated on every sync.

## Scaffolding (`shuttle scaffold`)

Rather than hand-write these files, `shuttle scaffold` generates them from the
same schema the loader uses — the output is validated before it's written, so it
always parses. Run it in the repo root (or pass `--repo <dir>`); the VS Code
extension wraps each of these in a command (see [editor support](editor#commands)).

```sh
# A service from a single image (writes services/web/{web.yaml,docker-compose.yml})
shuttle scaffold service web --host web1 --kind docker --image nginx:latest \
  --domain web.example.com --port 80

# A compose service (skeleton docker-compose.yml to edit) or a proxy-only service
shuttle scaffold service api --host web1 --kind compose
shuttle scaffold service infisical --host web1 --kind external \
  --upstream host.docker.internal:8222 --domain secrets.example.com

# A host (appended to hosts.yaml, preserving comments)
shuttle scaffold host web1 --label region=eu --label role=edge

# A DNS-challenge provider + a certificate (appended to dns.yml)
shuttle scaffold dns-provider ovh --type ovh --endpoint ovh-eu
shuttle scaffold certificate star --provider ovh --domain '*.example.com' --domain example.com
```

`host`, `dns-provider`, and `certificate` append to the existing file via a YAML
round-trip — your formatting and comments are kept, the entry lands in the right
list, and a duplicate name is refused. `dns-provider` prefills the credential
keys the provider type requires (e.g. OVH's `application_key` /
`application_secret` / `consumer_key`), each pointing at a secrets-provider key
you set. `service` refuses to overwrite an existing service.

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
  - repo_prefix: github.com/you/iac   # single repo; use a repo-scoped token
    infisical_key: IAC_REPO_TOKEN
```

For a private IaC repo, use a **repo-scoped** token (a GitHub fine-grained PAT /
deploy key, a GitLab project access token, …) limited to that one repo, not an
account-wide PAT — see [Git credentials](configuration.md#git-credentials).

A parse error is logged and old values are kept — a bad commit never blocks
deploys. `shuttle init` writes a commented starter file.

## `hosts.yaml`

```yaml
hosts:
  - name: web1            # must match the agent's --host
    labels:
      region: eu-west
      role: edge
    addresses:            # optional; targets for Shuttle-managed DNS records
      public: 203.0.113.20
      tailscale: 100.64.0.5
    caddy:                # optional; relocate the host's Caddy sidecar ports
      http_port: 8080     # default 80
      https_port: 8443    # default 443
```

- `name` is required and must be unique.
- `labels` are free-form metadata.
- `addresses` are the host's reachable IPs by **network label**, used as the
  target when Shuttle manages DNS records ([`dns.yml` zones](#dnsyml-optional)).
  A host can be reachable at several addresses — e.g. a `public` IP and a
  `tailscale` IP — and each zone names which label its records point at (default
  `public`). Only needed when you use DNS record management.
- `caddy.http_port` / `caddy.https_port` set the ports this host's Caddy sidecar
  listens on (and publishes). Useful when the host shares `:80`/`:443` with
  another process, or sits behind a load balancer that forwards to alternate
  ports. Both the container's internal listen and the host-published port use
  these values; they must differ and fall in `1–65535`. The orchestrator pushes
  the change on the next reconcile and the agent recreates its sidecar to remap
  the ports. **Note:** moving off `:80`/`:443` breaks ACME HTTP-01, so a
  relocated host should terminate TLS upstream or use the DNS challenge.

## `dns.yml` (optional)

Defines DNS-challenge certificate providers and the certificates they issue, so
service domains can be served by a **wildcard** certificate provisioned over an
ACME DNS-01 challenge — the only challenge that mints wildcards and that works
without the host being reachable on `:80`/`:443`.

```yaml
providers:
  - name: ovh                 # referenced by certificates below
    type: ovh                 # supported: cloudflare, ovh, route53
    endpoint: ovh-eu          # OVH API region (ovh-eu, ovh-ca, ...) — OVH only
    credentials:              # each value resolved from the secrets provider
      application_key:    { infisical_key: OVH_APPLICATION_KEY }
      application_secret: { infisical_key: OVH_APPLICATION_SECRET }
      consumer_key:       { infisical_key: OVH_CONSUMER_KEY }

certificates:
  - name: star-example        # referenced by a service's tls_certificate (optional)
    domains: ["*.example.com", "example.com"]
    provider: ovh
```

Each provider `type` dictates its `credentials:` keys (the editor and
`shuttle scaffold dns-provider` prefill them):

| `type` | `endpoint` | `credentials:` keys |
|--------|-----------|---------------------|
| `cloudflare` | — | `api_token` |
| `ovh` | required (`ovh-eu`, `ovh-ca`, …) | `application_key`, `application_secret`, `consumer_key` |
| `route53` | — | `access_key_id`, `secret_access_key`, `region` |

- A service whose domain falls under a certificate's `domains` (exact or
  wildcard, e.g. `app.example.com` under `*.example.com`) is **automatically**
  served by that certificate — no per-service config. A domain covered by **no**
  certificate keeps Caddy's default per-domain Let's Encrypt (HTTP-01).
- **Provider credentials are secrets**, never committed: each field references a
  key resolved from the secrets provider (`infisical_env`/`infisical_path`
  optionally override the lookup scope). The orchestrator resolves them per
  reconcile and injects them inline into the Caddy config it pushes over the
  TLS-protected agent stream — they never touch disk or the process argv. The
  same `backups.env`-style model as git credentials.
- A certificate is provisioned only on hosts that actually serve one of its
  domains (or pin it), so an unrelated host doesn't order a wildcard it never
  uses.
- **Requires a DNS-capable Caddy image.** The DNS challenge needs the provider
  plugin compiled into Caddy. Agents default their sidecar to
  `ghcr.io/neikow/shuttle-caddy` (stock Caddy + the Cloudflare, OVH, and Route53
  plugins); for a provider it doesn't bundle, build your own with `xcaddy` and
  point the agent at it with `--caddy-image`.
- `shuttle check` validates that every provider's credentials resolve.

### Record management (`zones:`)

Certificates above issue TLS; `zones:` makes Shuttle also **create the A/AAAA
records** that point a service's domains at its host — the manual step Caddy's
DNS modules don't do. Each zone maps a domain suffix to a record-management
provider and the host-address label its records target:

```yaml
zones:
  - domain: example.com         # public: portfolio etc. via OVH
    provider: ovh               # a providers[] entry (records-capable)
    address: public             # host addresses[] label (default "public")
  - domain: home.example.com    # private: homelab/Tailscale via the sidecar
    provider: home-sidecar
    address: tailscale
  - domain: legacy.net          # leave these to me
    provider: manual
```

- A service domain is matched to the **longest** zone suffix; that zone's
  provider then upserts `<domain> A/AAAA -> host.addresses[zone.address]`. This is
  how one project drives **split DNS** — public records via OVH, private
  (Tailscale) records via the sidecar.
- **Record-management providers** (the `zones[]` `provider`) are a separate
  capability from the ACME `certificates[]` providers. Supported today: **`ovh`**
  (via libdns) and **`manual`** (a no-op: Shuttle creates nothing, `shuttle
  check`/`plan` just list what *you* must create). The private-DNS **`sidecar`**
  provider is documented separately. `cloudflare`/`route53` remain ACME-only for
  now.
- **Ownership & drift.** Every managed record is paired with an owner TXT
  (`_shuttle-owner.<name>` = `heritage=shuttle`); Shuttle only ever updates or
  deletes records it owns — **foreign records are never touched**. The
  orchestrator catalogs owned records in its ledger and, on each reconcile,
  **heals** records changed out-of-band and **removes** ones whose service/domain
  left the repo. Removing a `zone` from `dns.yml` stops management (records are
  left as-is, not deleted).
- **Credentials are secrets** (same model as the cert providers) — resolved from
  the secrets provider per reconcile, never on disk. `shuttle check` lists every
  managed record (and flags a host missing the zone's address label).

## `services/<name>/<name>.yaml`

```yaml
name: whoami              # must equal the directory name
host: web1                # must reference a host in hosts.yaml
domains:                  # optional; drive Caddy ingress
  - whoami.example.com
env_from: production      # optional; Infisical environment to read secrets from
secret_path: /services/whoami  # optional; Infisical folder for this service (absolute)
env:                      # optional; the environment variables shipped to the service
  WHOAMI_NAME: ""           # "" -> read this key from the configured secrets provider
  DATABASE_URL: ${infisical:DB_URL}   # provider key under a different name
  LOG_LEVEL: ${env:LOG_LEVEL}         # the orchestrator's own process environment
  STATIC: info                        # a literal value
port: 80                  # optional; the Caddy upstream port for this service's domains
update_policy: rolling    # optional; "rolling" (default) or "recreate"
delete_volumes: manual    # optional; volume deletion policy on removal (default: manual)
caddy_snippet: |          # optional; JSON array of Caddy HTTP handlers
  [{"handler":"headers","response":{"set":{"X-Frame-Options":["DENY"]}}}]
tls_certificate: star-example  # optional; pin to a dns.yml certificate
backup:                   # optional; service data backup policy
  engine: volume          # "volume" (tar named volumes) or "postgres" (pg_dump)
  schedule: daily         # optional; hourly/daily/weekly or a duration ("12h")
  before_deploy: true     # optional; snapshot before each deploy (best-effort)
  store: restic           # optional; "restic" (default) or "local"; inherits backups.default_store
  target: "s3:s3.amazonaws.com/my-bucket/store"  # optional; inherits backups.default_target
  retention: { keep_daily: 7, keep_weekly: 4 }   # optional; restic forget policy
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
- **`env`** declares the environment variables shipped to the service. It is a
  map of variable name → **source spec**, resolved by the orchestrator at deploy
  time:
  - `""` (empty) — read this variable from the configured secrets provider,
    keyed by the variable name. (`WHOAMI_NAME: ""` reads `WHOAMI_NAME`.)
  - `${infisical:KEY}` / `${secret:KEY}` / `${KEY}` — read `KEY` from the
    configured provider (so you can ship a provider secret under a different
    variable name). `secret` and `infisical` are aliases; a bare `${KEY}` means
    the same.
  - `${env:KEY}` — read `KEY` from the **orchestrator's** own process
    environment.
  - anything else — a **literal** value. Tokens may be embedded in surrounding
    text (e.g. `https://${env:REGION}.example.com/${secret:PATH}`).

  A service with **no `env:`** reads nothing, performs **no provider lookup**,
  and therefore needs **no secrets folder to exist** — declaring a service that
  uses no secrets never forces you to create an (empty) Infisical entry for it.
  Any reference that doesn't resolve (a missing provider key, an unset
  `${env:KEY}`) is a **hard error** so a deploy fails loudly rather than shipping
  a blank value; `shuttle check` reports the same.
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
- **`tls_certificate`** optionally pins this service's domains to a certificate
  declared in [`dns.yml`](#dnsyml-optional), forcing its DNS challenge even when
  the domain wouldn't auto-match (e.g. an apex also reachable over HTTP-01). Must
  name a declared certificate. Usually unnecessary — a domain under a
  certificate's `domains` is served by it automatically.
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
- **`backup`** declares a data-backup policy for the service. The orchestrator
  backs up the service's *persistent data* (not its config — that lives in git):
  - **`engine`** (required) — `volume` tars the project's named Docker volumes;
    `postgres` runs `pg_dump`/`pg_dumpall` in the database container (set
    `db_service`, optionally `db_user`/`db_name`).
  - **`schedule`** — `hourly`, `daily`, `weekly`, or a duration (`"12h"`,
    `"7 days"`). Omit it for no scheduled backups (manual / pre-deploy still work).
  - **`before_deploy`** — when `true`, take a best-effort snapshot immediately
    before each deploy/rollback, so a bad release has a fresh restore point.
  - **`store`** — `restic` (default; dedup + encryption, local path or S3/B2/…) or
    `local` (a plain tar/SQL file on the host). Inherits `backups.default_store`.
  - **`target`** — the restic repository or local directory. Inherits
    `backups.default_target`.
  - **`retention`** — `keep_last`/`keep_daily`/`keep_weekly`/`keep_monthly` passed
    to `restic forget` after each backup (restic store only).

  Backend credentials (restic password, S3 keys) are **not** declared here — they
  are secrets, resolved from the secrets provider via the orchestrator's
  `backups.env` (see [configuration.md](configuration.md#backups)). The host needs
  the `restic` path / `docker` reachable to the agent.

## Service source — exactly one

Each service must declare **exactly one** source (XOR), or the sync fails:

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
at `path`.

### External (proxy-only)

An `external:` block for a service Shuttle should **route to but not deploy** —
e.g. infrastructure running beside the agent that you bootstrap out-of-band (an
Infisical instance, a database UI, anything already running):

```yaml
name: infisical
host: web1
domains: ["infisical.example.com"]
external:
  upstream: "infisical:8080"   # dialed verbatim by the host's Caddy sidecar
```

Shuttle skips it in every lifecycle path — no deploy, diff, drift heal, teardown,
or backup — and only emits a **Caddy route** for it: HTTPS (HTTP-01, or a
[`dns.yml`](#dnsyml-optional) wildcard) + reverse proxy to `upstream`.
`caddy_snippet` and `tls_certificate` apply as normal; `port`, `env`,
`backup`, and `update_policy` are not used.

**You** make `upstream` reachable from the Caddy sidecar (a container on the
shared `shuttle` docker network). The simplest way is to attach the external
container to that network (`networks: [shuttle]` as an external network in its own
compose) so `name:port` resolves; otherwise use `host.docker.internal:port` via a
host-gateway. The upstream is plain HTTP (Caddy terminates TLS and proxies HTTP
to it).

Declaring more than one source is a validation error ("XOR violation"); declaring
none is also an error.

## How it maps to a deploy

On each reconcile the orchestrator loads this repo, diffs every service's source
SHA against the ledger, and for changed services renders the compose + env and
dispatches a deploy to the service's `host`. The current commit SHA is what the
ledger records and what rollback targets.
