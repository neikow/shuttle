#!/usr/bin/env bash
# Provisions a Gitea test repo and registers a repo webhook with the orchestrator.
# Requires: curl, git, docker. Run 'make dev-gitea' and start the orchestrator first.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

GITEA_URL="${GITEA_URL:-http://localhost:3000}"
ORCHESTRATOR_URL="${ORCHESTRATOR_URL:-http://localhost:8099}"
ORCHESTRATOR_TOKEN="${ORCHESTRATOR_TOKEN:-test-bearer}"
GITEA_USER="${GITEA_USER:-shuttle-dev}"
GITEA_PASS="${GITEA_PASS:-shuttledev123}"
REPO_NAME="${REPO_NAME:-remote-app}"
SERVICE_NAME="${SERVICE_NAME:-remote-app}"

echo "Waiting for Gitea at $GITEA_URL..."
until curl -sf "$GITEA_URL" > /dev/null 2>&1; do sleep 1; done
echo "Gitea is up."

# Create admin user (idempotent: ignore error if already exists)
docker compose -f "$SCRIPT_DIR/docker-compose.gitea.yml" exec -T gitea \
  gitea admin user create \
    --username "$GITEA_USER" \
    --password "$GITEA_PASS" \
    --email "dev@shuttle.local" \
    --must-change-password=false \
    --admin 2>/dev/null || true

# Create repo (idempotent: ignore conflict)
curl -sf -u "$GITEA_USER:$GITEA_PASS" \
  -X POST "$GITEA_URL/api/v1/user/repos" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"$REPO_NAME\",\"private\":false,\"auto_init\":false}" > /dev/null || true

# Push seed fixtures
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT
cp -r "$REPO_ROOT/test/fixtures/remote-repo" "$TMPDIR/repo"
git -C "$TMPDIR/repo" init -q -b main
git -C "$TMPDIR/repo" add -A
git -C "$TMPDIR/repo" -c user.email=dev@shuttle.local -c user.name=shuttle-dev commit -qm "seed remote-app"
git -C "$TMPDIR/repo" remote add origin \
  "http://$GITEA_USER:$GITEA_PASS@$(echo "$GITEA_URL" | sed 's|http://||')/$GITEA_USER/$REPO_NAME.git"
git -C "$TMPDIR/repo" push -u origin main --force
echo "Pushed fixture to $GITEA_URL/$GITEA_USER/$REPO_NAME"

# Register the repo webhook with the orchestrator
echo "Creating repo webhook for service '$SERVICE_NAME'..."
WEBHOOK_RESPONSE=$(curl -sf \
  -X POST "$ORCHESTRATOR_URL/webhooks/repo" \
  -H "Authorization: Bearer $ORCHESTRATOR_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"service\":\"$SERVICE_NAME\"}")
WEBHOOK_ID=$(echo "$WEBHOOK_RESPONSE" | grep -o '"id":"[^"]*"' | cut -d'"' -f4)
if [ -z "$WEBHOOK_ID" ]; then
  echo "ERROR: failed to create webhook. Orchestrator response: $WEBHOOK_RESPONSE" >&2
  exit 1
fi
WEBHOOK_URL="$ORCHESTRATOR_URL/webhook/repo/$WEBHOOK_ID"

# Register the webhook URL with the Gitea repo
echo "Registering webhook with Gitea repo $GITEA_USER/$REPO_NAME..."
curl -sf \
  -u "$GITEA_USER:$GITEA_PASS" \
  -X POST "$GITEA_URL/api/v1/repos/$GITEA_USER/$REPO_NAME/hooks" \
  -H "Content-Type: application/json" \
  -d "{
    \"type\": \"gitea\",
    \"active\": true,
    \"events\": [\"push\"],
    \"config\": {
      \"url\": \"$WEBHOOK_URL\",
      \"content_type\": \"json\"
    }
  }" > /dev/null
echo "Gitea webhook registered."

echo ""
echo "Setup complete."
echo "  Gitea repo:  $GITEA_URL/$GITEA_USER/$REPO_NAME"
echo "  Webhook URL: $WEBHOOK_URL"
echo ""
echo "Push a commit to $REPO_NAME to trigger a deploy of '$SERVICE_NAME'."
