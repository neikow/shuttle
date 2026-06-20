#!/bin/sh
# Simulated remote host for the dev cluster: run an isolated Docker daemon
# (Docker-in-Docker), then enroll this host with the orchestrator (mint a
# single-use join token over the control plane, redeem it) and run the shuttle
# agent against the LOCAL daemon. Persisted credentials are reused on restart.
#
# DEV ONLY: the host holds the bootstrap bearer to mint its own join token, and
# the whole control plane runs over plaintext. Never do this in production.
set -u

: "${HOST:?HOST env required}"
ORCH_URL="${ORCH_URL:-http://orchestrator:8080}"
ORCH_GRPC="${ORCH_GRPC:-orchestrator:9090}"
BEARER="${BEARER:-test-bearer}"
WORK=/var/lib/shuttle-agent
mkdir -p "$WORK"

echo "[$HOST] starting docker daemon (dind)..."
dockerd-entrypoint.sh dockerd >/var/log/dockerd.log 2>&1 &

echo "[$HOST] waiting for local docker daemon..."
until docker info >/dev/null 2>&1; do sleep 1; done
echo "[$HOST] docker ready"

# Restart path: credentials already persisted by a previous join.
if [ -f "$WORK/agent.token" ]; then
	echo "[$HOST] existing credentials found; starting agent"
	exec shuttle agent --orchestrator "$ORCH_GRPC" --host "$HOST" --work-dir "$WORK"
fi

echo "[$HOST] waiting for orchestrator at $ORCH_URL ..."
until curl -fsS "$ORCH_URL/healthz" >/dev/null 2>&1; do sleep 1; done

# Mint a join token for this host. The host must already exist in the IaC repo,
# so retry until the orchestrator has synced the repo on first boot.
echo "[$HOST] enrolling..."
JT=""
while [ -z "$JT" ]; do
	JT=$(curl -fsS -X POST \
		-H "Authorization: Bearer $BEARER" \
		-H "Content-Type: application/json" \
		-d "{\"host\":\"$HOST\"}" \
		"$ORCH_URL/enroll" 2>/dev/null | jq -r '.join_token // empty')
	[ -z "$JT" ] && { echo "[$HOST] host not registered yet / repo syncing; retrying..."; sleep 3; }
done

echo "[$HOST] redeeming join token and starting agent"
exec shuttle agent join --redeem-url "$ORCH_URL" --token "$JT" --work-dir "$WORK"
