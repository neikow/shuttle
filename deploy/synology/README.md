# Running a Shuttle agent on Synology DSM

A Synology NAS is just another managed host. The agent runs natively on DSM and
drives deploys through **Container Manager** (DSM 7.2+), which ships the Docker
CLI and the `compose` plugin.

## Why a dedicated driver

DSM installs the Docker CLI at `/usr/local/bin/docker`. Task Scheduler runs jobs
with a minimal `PATH` that excludes it, so `docker` is not found. The `synology`
driver (`--driver synology`) invokes that absolute path. Override it with
`--docker-bin` if your install differs.

In every other respect the Synology agent behaves exactly like a standard
compose host: it receives a rendered compose file + env from the orchestrator
and runs `docker compose up -d`.

## Prerequisites

- DSM 7.2 or later with the **Container Manager** package installed.
- A `shuttle` binary for your NAS architecture (`linux/amd64` for Intel models,
  `linux/arm64` for ARM). Grab it from a release or `make build`.
- A host entry in your IaC repo's `hosts.yaml` whose `name` matches the agent's
  `--host` (e.g. `nas1`).

## Install

1. Copy the binary to `/usr/local/bin/shuttle` (over SSH or File Station).
2. If using mTLS, place `agent.crt`, `agent.key`, `ca.crt` under
   `/etc/shuttle/certs`. Otherwise drop the `--cert/--key/--ca` flags.
3. Copy `shuttle-agent.sh` to a persistent volume, e.g.
   `/volume1/shuttle/shuttle-agent.sh`, and edit the orchestrator address,
   `--host`, and work dir.
4. **Control Panel → Task Scheduler → Create → Triggered Task → User-defined
   script.** Set User `root`, Event `Boot-up`, and Command:
   ```
   sh /volume1/shuttle/shuttle-agent.sh
   ```
5. Select the task and **Run** to start it now (it also starts on every reboot).

## Verify

On the orchestrator you should see `agent connected host=nas1`. Trigger a deploy
targeting that host and confirm the container appears under Container Manager.

## Notes

- Keep `--work-dir` on a real volume (`/volume1/...`); the agent writes the
  rendered compose file and `.env` there.
- Task Scheduler restarts the script if the NAS reboots but not if the process
  exits. For auto-restart, wrap the `exec` line in a `while true; do … ; sleep 5;
  done` loop, or run the agent as a container under Container Manager with the
  Docker socket mounted (use `--driver compose` in that case, since the CLI is on
  `PATH` inside the image).
