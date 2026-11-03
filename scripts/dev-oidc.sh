#!/usr/bin/env bash
# Start a local Dex instance, seed demo RBAC, then run the portal with OIDC.
# This is a local demo, not a production deployment.
set -euo pipefail
cd "$(dirname "$0")/.."

KUBE_CONTEXT="${KUBE_CONTEXT:-local}"
NAMESPACE="${PODDY_NAMESPACE:-poddy}"
PORTAL_ADDR="${PODDY_PORTAL_ADDR:-:8090}"
PORTAL_URL="${PODDY_PORTAL_URL:-http://127.0.0.1:8090}"
DEX_URL="http://127.0.0.1:5556/dex"

for command in docker kubectl curl go; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "missing required command: $command" >&2
    exit 1
  fi
done

if ! kubectl --context "$KUBE_CONTEXT" get namespace "$NAMESPACE" >/dev/null 2>&1; then
  echo "namespace $NAMESPACE not found in context $KUBE_CONTEXT" >&2
  echo "set KUBE_CONTEXT and PODDY_NAMESPACE, or create the namespace first" >&2
  exit 1
fi

if curl -fsS "$PORTAL_URL/healthz" >/dev/null 2>&1; then
  echo "a portal is already running at $PORTAL_URL; stop it first" >&2
  exit 1
fi

echo "Starting Dex..."
docker compose -f deploy/dex/docker-compose.yaml up -d

echo "Waiting for Dex at $DEX_URL ..."
ready=false
for _ in $(seq 1 30); do
  if curl -fsS "$DEX_URL/.well-known/openid-configuration" >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 1
done
if [ "$ready" != true ]; then
  docker compose -f deploy/dex/docker-compose.yaml logs --tail=80 dex >&2 || true
  echo "Dex did not become ready" >&2
  exit 1
fi

echo "Applying demo RBAC to $NAMESPACE/poddy-rbac (overwrites it)..."
sed "s/namespace: poddy/namespace: ${NAMESPACE}/" deploy/dex/rbac.yaml |
  kubectl --context "$KUBE_CONTEXT" apply -f -

echo "Building the portal..."
go build -o poddy .

cat <<INFO

Dex:    $DEX_URL
Portal: $PORTAL_URL

Users:
  admin@example.com / admin   global admin
  ops@example.com   / ops     global admin
  dev@example.com   / poddy  owner of the go template
  alice@example.com / alice   member of the backend team
  bob@example.com   / bob     member of the backend team
  carol@example.com / carol   member of the frontend team
  dave@example.com  / dave    direct user of the go template
  erin@example.com  / erin    regular user (base template only)

Demo:
  1. Sign in as dev, open Templates, choose go, Manage access, tick backend.
  2. Sign out and sign in as alice or bob: the go template is now available.
  3. dave already has direct access to go; carol and erin see only base.
  4. Sign in as admin or ops to create teams or see every workspace.

Stop Dex: docker compose -f deploy/dex/docker-compose.yaml down

INFO

exec ./poddy \
  -addr="$PORTAL_ADDR" \
  -context "$KUBE_CONTEXT" \
  -namespace "$NAMESPACE" \
  -token=secret \
  -oidc-issuer="$DEX_URL" \
  -oidc-client-id=poddy \
  -oidc-client-secret=poddy-secret \
  -oidc-redirect-url="${PORTAL_URL%/}/oidc/callback" \
  -oidc-session-key=poddy-demo-session-key-change-me \
  -ssh-addr=127.0.0.1:2222 \
  -ssh-public-host=127.0.0.1 \
  -ssh-public-port=2222
