#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
KUBECONFIG="${PODDY_KUBECONFIG:-/tmp/opencode/k3s.yaml}"
export KUBECONFIG

NS="${PODDY_NAMESPACE:-poddy-system}"
WS_NS="${PODDY_WORKSPACE_NAMESPACE:-poddy-workspaces}"
HOST="${DEMO_HOST:-$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')}"
IMAGE_TAG="${IMAGE_TAG:-demo-$(git -C "$ROOT" rev-parse --short HEAD)}"
STATE_DIR="${STATE_DIR:-/tmp/opencode}"

GITEA_ADMIN_USER="${GITEA_ADMIN_USER:-poddy}"
GITEA_ADMIN_PASSWORD="${GITEA_ADMIN_PASSWORD:-poddy-demo}"
GITEA_ADMIN_EMAIL="${GITEA_ADMIN_EMAIL:-poddy@example.com}"
MINIO_USER="${MINIO_USER:-minioadmin}"
MINIO_PASSWORD="${MINIO_PASSWORD:-minioadmin}"

mkdir -p "$STATE_DIR"

if [[ "${SKIP_BUILD:-0}" != "1" ]]; then
  make -C "$ROOT" image IMAGE_TAG="$IMAGE_TAG"
fi

echo "==> Demo host: $HOST"
echo "==> Portal image: ghcr.io/slimaineb/poddy:$IMAGE_TAG"

kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl create namespace "$WS_NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

secret_value() {
  kubectl -n "$NS" get secret "$1" -o jsonpath="{.data.$2}" 2>/dev/null | base64 -d
}

if ! kubectl -n "$NS" get secret minio-credentials >/dev/null 2>&1; then
  kubectl -n "$NS" create secret generic minio-credentials \
    --from-literal=root-user="$MINIO_USER" \
    --from-literal=root-password="$MINIO_PASSWORD" >/dev/null
else
  MINIO_USER="$(secret_value minio-credentials root-user)"
  MINIO_PASSWORD="$(secret_value minio-credentials root-password)"
fi

kubectl -n "$NS" create secret generic poddy-backup \
  --from-literal=access-key="$MINIO_USER" \
  --from-literal=secret-key="$MINIO_PASSWORD" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null

if ! kubectl -n "$NS" get secret poddy-postgres >/dev/null 2>&1; then
  kubectl -n "$NS" create secret generic poddy-postgres \
    --from-literal=password="$(openssl rand -hex 24)" >/dev/null
fi

if ! kubectl -n "$NS" get secret poddy-database >/dev/null 2>&1; then
  POSTGRES_PASSWORD="$(secret_value poddy-postgres password)"
  kubectl -n "$NS" create secret generic poddy-database \
    --from-literal=database-url="postgres://poddy:${POSTGRES_PASSWORD}@poddy-postgres.${NS}.svc.cluster.local:5432/poddy?sslmode=disable" >/dev/null
fi

if ! kubectl -n "$NS" get secret poddy-auth >/dev/null 2>&1; then
  kubectl -n "$NS" create secret generic poddy-auth \
    --from-literal=token="$(openssl rand -hex 32)" >/dev/null
fi

if ! kubectl -n "$NS" get secret poddy-oidc >/dev/null 2>&1; then
  kubectl -n "$NS" create secret generic poddy-oidc \
    --from-literal=oidc-client-id=poddy \
    --from-literal=oidc-client-secret=poddy-secret \
    --from-literal=oidc-session-key="$(openssl rand -hex 32)" >/dev/null
fi

if ! kubectl -n "$NS" get secret poddy-ssh-ca >/dev/null 2>&1; then
  CA_DIR="$(mktemp -d)"
  ssh-keygen -q -t ed25519 -N "" -C poddy-demo-ca -f "$CA_DIR/ca" >/dev/null
  kubectl -n "$NS" create secret generic poddy-ssh-ca \
    --from-file=ssh-ca-key="$CA_DIR/ca" >/dev/null
  rm -rf "$CA_DIR"
fi

if ! kubectl -n "$NS" get secret poddy-ssh-keys >/dev/null 2>&1; then
  kubectl -n "$NS" create secret generic poddy-ssh-keys \
    --from-literal=authorized_keys="" >/dev/null
fi

if ! kubectl -n "$NS" get secret poddy-external-auth-config >/dev/null 2>&1; then
  kubectl -n "$NS" create secret generic poddy-external-auth-config \
    --from-literal=external-auth-key="$(openssl rand -hex 32)" >/dev/null
fi
EXTERNAL_AUTH_KEY="$(secret_value poddy-external-auth-config external-auth-key)"

echo "==> Applying Dex and PostgreSQL"
sed "s/__DEMO_HOST__/${HOST}/g" "$ROOT/deploy/demo/dex.yaml" | kubectl apply -f - >/dev/null
kubectl apply -f "$ROOT/deploy/demo/postgres.yaml" >/dev/null
kubectl -n "$NS" rollout restart deploy/dex >/dev/null
kubectl -n "$NS" rollout status deploy/dex --timeout=180s
kubectl -n "$NS" rollout status statefulset/poddy-postgres --timeout=300s

echo "==> Applying MinIO and Gitea"
kubectl apply -f "$ROOT/deploy/demo/minio.yaml" >/dev/null
sed "s/__DEMO_HOST__/${HOST}/g" "$ROOT/deploy/demo/gitea.yaml" | kubectl apply -f - >/dev/null
kubectl -n "$NS" rollout status deploy/minio --timeout=300s
kubectl -n "$NS" rollout status deploy/gitea --timeout=300s

for i in $(seq 1 60); do
  if curl -fsS "http://${HOST}:30300/api/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 2
  if [[ "$i" == "60" ]]; then
    echo "Gitea did not become reachable" >&2
    exit 1
  fi
done

echo "==> Creating Gitea admin and OAuth2 application"
if ! kubectl -n "$NS" exec deploy/gitea -- \
  su-exec git gitea admin user list --config /data/gitea/conf/app.ini 2>/dev/null | grep -q "${GITEA_ADMIN_USER}"; then
  kubectl -n "$NS" exec deploy/gitea -- \
    su-exec git gitea admin user create --admin \
    --username "$GITEA_ADMIN_USER" \
    --password "$GITEA_ADMIN_PASSWORD" \
    --email "$GITEA_ADMIN_EMAIL" \
    --must-change-password=false \
    --config /data/gitea/conf/app.ini >/dev/null
fi

GITEA_API="http://${HOST}:30300/api/v1"
EXISTING_APP_ID="$(curl -fsS -u "${GITEA_ADMIN_USER}:${GITEA_ADMIN_PASSWORD}" \
  "${GITEA_API}/user/applications/oauth2" | jq -r '.[]? | select(.name == "poddy") | .id' | head -n1 || true)"
if [[ -n "$EXISTING_APP_ID" ]]; then
  curl -fsS -u "${GITEA_ADMIN_USER}:${GITEA_ADMIN_PASSWORD}" \
    -X DELETE "${GITEA_API}/user/applications/oauth2/${EXISTING_APP_ID}" >/dev/null
fi

APP_JSON="$(curl -fsS -u "${GITEA_ADMIN_USER}:${GITEA_ADMIN_PASSWORD}" \
  -H 'Content-Type: application/json' \
  -X POST "${GITEA_API}/user/applications/oauth2" \
  -d "{\"name\":\"poddy\",\"redirect_uris\":[\"http://${HOST}:30081/external-auth/gitea/callback\"],\"confidential_client\":true}")"
GITEA_CLIENT_ID="$(jq -r '.client_id' <<<"$APP_JSON")"
GITEA_CLIENT_SECRET="$(jq -r '.client_secret' <<<"$APP_JSON")"
if [[ -z "$GITEA_CLIENT_ID" || "$GITEA_CLIENT_ID" == "null" || -z "$GITEA_CLIENT_SECRET" || "$GITEA_CLIENT_SECRET" == "null" ]]; then
  echo "Could not create Gitea OAuth2 application: $APP_JSON" >&2
  exit 1
fi

kubectl -n "$NS" create secret generic poddy-external-auth-config \
  --from-literal=external-auth-key="$EXTERNAL_AUTH_KEY" \
  --from-literal=provider-gitea-client-secret="$GITEA_CLIENT_SECRET" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null

VALUES_FILE="${STATE_DIR}/poddy-demo-values.yaml"
PROVIDER_FILE="${STATE_DIR}/poddy-demo-provider.yaml"
sed "s/__DEMO_HOST__/${HOST}/g" "$ROOT/deploy/demo/values.yaml" > "$VALUES_FILE"
cat > "$PROVIDER_FILE" <<EOF
externalAuth:
  providers:
    - id: gitea
      type: gitea
      displayName: Gitea
      clientId: "${GITEA_CLIENT_ID}"
      existingSecret: poddy-external-auth-config
      clientSecretKey: provider-gitea-client-secret
      authUrl: http://${HOST}:30300/login/oauth/authorize
      tokenUrl: http://gitea-http.${NS}.svc.cluster.local:3000/login/oauth/access_token
      regex: '^(https?://)?${HOST}:30300(/.*)?$'
      scopes: [read:user, write:repository]
      env: [GITEA_TOKEN]
EOF

echo "==> Installing poddy templates and portal"
helm --kubeconfig "$KUBECONFIG" upgrade --install poddy-templates "$ROOT/deploy/helm/poddy-templates" \
  -n "$WS_NS" -f "$ROOT/deploy/demo/templates-values.yaml" \
  --wait --timeout 5m >/dev/null

helm --kubeconfig "$KUBECONFIG" upgrade --install poddy "$ROOT/deploy/helm/poddy" \
  -n "$NS" -f "$VALUES_FILE" -f "$PROVIDER_FILE" \
  --set image.tag="$IMAGE_TAG" --set image.pullPolicy=IfNotPresent \
  --wait --timeout 10m >/dev/null

kubectl -n "$NS" rollout status deploy/poddy --timeout=300s >/dev/null
for i in $(seq 1 60); do
  if curl -fsS "http://${HOST}:30081/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 2
  if [[ "$i" == "60" ]]; then
    echo "poddy did not become reachable" >&2
    exit 1
  fi
done

PORTAL_TOKEN="$(secret_value poddy-auth token)"
cat > "${STATE_DIR}/poddy-demo.env" <<EOF
DEMO_HOST=${HOST}
PORTAL_URL=http://${HOST}:30081
PORTAL_TOKEN=${PORTAL_TOKEN}
GITEA_URL=http://${HOST}:30300
GITEA_ADMIN_USER=${GITEA_ADMIN_USER}
GITEA_ADMIN_PASSWORD=${GITEA_ADMIN_PASSWORD}
GITEA_ADMIN_EMAIL=${GITEA_ADMIN_EMAIL}
EOF

cat <<EOF

Demo is ready.

  Portal:        http://${HOST}:30081
  Dex:           http://${HOST}:30556/dex
  Gitea:         http://${HOST}:30300
  MinIO console: http://${HOST}:30901

  Gitea admin:   ${GITEA_ADMIN_USER} / ${GITEA_ADMIN_PASSWORD}
  Dex users:     admin/admin, dev/poddy, alice/alice, ops/ops, bob/bob, carol/carol, dave/dave, erin/erin
  Portal token:  ${PORTAL_TOKEN}

Run deploy/demo/verify.py to prove external auth and backup/restore.
EOF
