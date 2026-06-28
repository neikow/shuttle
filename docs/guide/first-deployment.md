# Deploy to a real host

This takes you from an installed binary to a service running on **your own
server**, deployed from git. The whole thing is four steps: bootstrap, start the
orchestrator, enroll a host, commit a service.

::: tip Prerequisites
[Install the `shuttle` binary](/guide/installation) on your control host and on
the server you'll deploy to. The server needs Docker with Compose v2.
:::

## 1. Bootstrap

For a real deployment the IaC repo lives in git and the orchestrator runs on a
server, so bootstrap spans **two machines**.

On your **workstation**, scaffold the repo (choosing the CI provider you'll push
to), create an empty repo on your provider, and publish:

```sh
shuttle init --ci github          # or: gitlab / none — scaffolds the IaC repo
git remote add origin git@github.com:you/iac.git
git push -u origin main
```

On the **orchestrator server**, generate the server config from that remote URL —
a bearer token + webhook secret and `config.yml` (mode 0600):

```sh
shuttle orchestrator init --repo-url https://github.com/you/iac.git
```

`orchestrator init` is **secure by default** — press Enter through it and you get
TLS with SSH-like token enrollment, including a self-signed orchestrator cert
generated for you (no `openssl`, no CA to distribute). `--repo-url` is also how
you point at an IaC repo you already have. Pass `--advanced` for the secrets
provider, mutual TLS, an insecure local link, and the externally reachable
control URL. Re-running is safe; it never overwrites your edits.

::: tip Deploying to a remote server
For a real host, run `shuttle orchestrator init --advanced` and set the
**externally reachable control URL** to your public HTTPS endpoint (e.g.
`https://orchestrator.example.com:8080`) when asked — `enroll` uses it to pin the
orchestrator's cert in the join command, and CI reads it too.
:::

::: details Prefer to write the config by hand?
A minimal `config.yml`:

```yaml
bearer_token: "<generate-a-long-random-string>"
http_addr: ":8080"
grpc_addr: ":9090"
data_dir: /var/lib/shuttle
repo_url: "https://github.com/you/your-iac-repo.git"
repo_branch: "main"
webhook_secret: "<another-random-string>"
agent_token_auth: true
advertise_addr: "orchestrator.example.com:9090"
advertise_control_url: "https://orchestrator.example.com:8080"
```

See the [Configuration reference](/configuration) for every key.
:::

## 2. Start the orchestrator

```sh
shuttle orchestrator --config config.yml
```

It's now serving the control plane on `:8080` and waiting for agents on `:9090`.
A systemd unit template lives at `deploy/systemd/shuttle-orchestrator.service`.

## 3. Enroll your server

First declare the host in your IaC repo's `hosts.yaml`:

```yaml
hosts:
  - name: web1
    labels: { region: eu-west }
```

Then, from the control host, mint a single-use join command:

```sh
shuttle enroll --config config.yml --host web1
```

It prints a ready-to-run, certificate-pinned one-liner. **Run that on `web1`:**

```sh
shuttle agent join --redeem-url https://orchestrator.example.com:8080 \
  --token <join-token> --pin sha256:<pin>
```

`join` redeems the token, persists its credentials, and starts the agent — which
dials *out* to the orchestrator (no inbound ports on `web1`). Back in the control
plane the host shows up connected. See
[Enrolling agents](/operations#enrolling-agents-with-tokens) for the mTLS and
manual-token variants.

## 4. Add a service and deploy

A service is two files in your IaC repo:

```
services/whoami/whoami.yaml          # which host, which domain, which port
services/whoami/docker-compose.yml   # what to run
```

`services/whoami/whoami.yaml`:

```yaml
name: whoami
host: web1
domains: ["whoami.example.com"]   # omit for no public ingress
port: 80
```

`services/whoami/docker-compose.yml`:

```yaml
services:
  whoami:
    image: traefik/whoami:latest
    restart: unless-stopped
```

Commit and push:

```sh
git add services/whoami
git commit -m "deploy whoami"
git push
```

If you scaffolded CI (`shuttle init --ci github|gitlab`) and set its repo
variables, the push deploys immediately;
otherwise the orchestrator's reconcile loop picks it up within ~60s (or trigger it
now with `shuttle plan` to preview, then a manual deploy). The agent pulls the
image, runs it, and reports back — and if you set `domains`, Caddy gets a route
with automatic HTTPS.

## Verify

```sh
# from the control host
curl -s -H "Authorization: Bearer $BEARER_TOKEN" \
  https://orchestrator.example.com:8080/overview | jq

# or just open the UI
open https://orchestrator.example.com:8080/ui/
```

You should see `web1` connected with `whoami` running. If you set a domain and
DNS points at the host, `https://whoami.example.com` serves it.

## From here

- [IaC repository schema](/iac-repo) — every service + host field.
- [Configuration reference](/configuration) — secrets, OIDC, Caddy, git creds.
- [Operations](/operations) — mTLS, Synology, backups, releases.
- [HTTP API](/http-api) — deploy, rollback, plan, and webhook endpoints.
