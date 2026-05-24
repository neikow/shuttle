#!/usr/bin/env bash
# Provisions a test private repo in the local Gitea instance.
# Requires: curl, git, docker. Gitea must be running (make dev-gitea).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

GITEA_URL="${GITEA_URL:-http://localhost:3000}"
GITEA_USER="${GITEA_USER:-shuttle-dev}"
GITEA_PASS="${GITEA_PASS:-shuttledev123}"
REPO_NAME="${REPO_NAME:-remote-app}"

echo "Waiting for Gitea..."
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

# Create a long-lived API token for the user
TOKEN=$(curl -sf -u "$GITEA_USER:$GITEA_PASS" \
  -X POST "$GITEA_URL/api/v1/users/$GITEA_USER/tokens" \
  -H "Content-Type: application/json" \
  -d '{"name":"shuttle-dev-token"}' | grep -o '"sha1":"[^"]*"' | cut -d'"' -f4 || true)

# Create private repo (idempotent: ignore conflict)
curl -sf -u "$GITEA_USER:$GITEA_PASS" \
  -X POST "$GITEA_URL/api/v1/user/repos" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"$REPO_NAME\",\"private\":true,\"auto_init\":false}" > /dev/null || true

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
echo "Pushed fixture to $GITEA_URL/$GITEA_USER/$REPO_NAME (private)"

echo ""
echo "Gitea repo ready: $GITEA_URL/$GITEA_USER/$REPO_NAME"
if [ -n "${TOKEN:-}" ]; then
  echo ""
  echo "Store this token in Infisical as GITEA_TOKEN, then add to orchestrator config:"
  echo "  git_credentials:"
  echo "    - repo_prefix: \"localhost:3000\""
  echo "      infisical_key: GITEA_TOKEN"
  echo ""
  echo "Token: $TOKEN"
fi
