#!/bin/sh
# Synology boot-up task: run the Shuttle agent against DSM Container Manager's Docker.
#
# Install:
#   1. Copy the linux/amd64 (or arm64) `shuttle` binary to /usr/local/bin/shuttle.
#   2. Place certs under /etc/shuttle/certs (or drop the --cert/--key/--ca flags
#      for an insecure dev link).
#   3. Control Panel > Task Scheduler > Create > Triggered Task > User-defined script.
#      User: root. Event: Boot-up. Command: sh /volume1/shuttle/shuttle-agent.sh
#
# --driver synology points at /usr/local/bin/docker, where DSM Container Manager
# (DSM 7.2+) installs the Docker CLI with the compose plugin; Task Scheduler runs
# with a minimal PATH that does not include it.
set -eu

exec /usr/local/bin/shuttle agent \
  --driver synology \
  --orchestrator orchestrator.example.com:9090 \
  --host nas1 \
  --work-dir /volume1/shuttle/agent \
  --cert /etc/shuttle/certs/agent.crt \
  --key /etc/shuttle/certs/agent.key \
  --ca /etc/shuttle/certs/ca.crt
