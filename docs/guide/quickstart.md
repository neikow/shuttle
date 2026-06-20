# Quickstart — running in 3 minutes

Spin up a complete Shuttle environment — orchestrator, web UI, two managed hosts,
and a live service instance — with **one command**. No cloud account, no config
to write, nothing to enroll by hand.

## What you need

- **Docker** with Compose v2 (`docker compose version`)
- **git**

That's it. You don't need Go installed — everything builds inside containers.

## 1. Get it running

```sh
git clone https://github.com/neikow/shuttle.git
cd shuttle
make dev-up
```

The first `make dev-up` builds the images (~2 min on a cold machine); after that
it's seconds. When it finishes you have:

- an **orchestrator** + embedded **web UI** on <http://localhost:8080/ui/>
- **two managed hosts** (`web1`, `web2`) — each an isolated Docker engine running
  a self-enrolling agent, mirroring real remote servers
- a seeded **IaC repo** at `.dev-cluster/iac/` declaring a sample `web` and `api`
  service

## 2. Watch your first instance come up

Open <http://localhost:8080/ui/> and paste the token **`test-bearer`**.

- The **Overview** tab shows `web1` and `web2` connecting.
- Within ~60s the orchestrator pulls the IaC repo and deploys the sample `web`
  service (a `traefik/whoami` container) onto `web1`. It flips to **running**.
- The **Deploys** tab records the deploy in the append-only ledger.

You now have an orchestrator and a running service instance. 🎉

## 3. Change something and redeploy

The IaC repo is just git. Edit a service, commit, and the reconciler rolls it out
within ~60s — exactly how a real deploy works.

```sh
cd .dev-cluster/iac

# change the service's displayed name
sed -i '' 's/web@web1/hello-shuttle/' services/web/docker-compose.yml   # macOS
# (Linux: sed -i 's/web@web1/hello-shuttle/' services/web/docker-compose.yml)

git commit -am "rename whoami instance"
```

Watch the **Deploys** tab: a new deploy appears, and the **Rollback** button next
to the previous one will put it back with a single click.

## 4. Tear it down

```sh
make dev-down
```

Removes the containers, their volumes, and the seeded repo.

## What just happened

This is the full Shuttle pipeline, end to end:

1. You committed to the **IaC repo** → the orchestrator detected the new SHA.
2. It diffed desired (repo) vs actual (ledger), **rendered** the service's compose
   file, and dispatched it down the gRPC stream to the agent on `web1`.
3. The **agent** ran `docker compose up` in its own engine and reported the result.
4. The orchestrator recorded it in the **ledger** — which is what makes the
   one-click rollback possible.

See [How it works](/guide/getting-started#how-it-works) for the architecture, or
go straight to a real deployment:

- [Installation](/guide/installation) — install the `shuttle` binary or image.
- [Deploy to a real host](/guide/first-deployment) — your own server, in minutes.
