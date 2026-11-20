#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
KUBECONFIG="${PODDY_KUBECONFIG:-/tmp/opencode/k3s.yaml}"
export KUBECONFIG

NS="${PODDY_NAMESPACE:-poddy-system}"
HOST="${DEMO_HOST:-$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')}"
IMAGE_TAG="${IMAGE_TAG:-dev}"
REGIONS="${DEMO_REGIONS:-eu us}"
HEADSCALE_PORT="${HEADSCALE_PORT:-30090}"
STUN_PORT="${STUN_PORT:-30347}"
STATE_DIR="${STATE_DIR:-/tmp/opencode}"
mkdir -p "$STATE_DIR"

echo "==> Demo host: $HOST"
echo "==> Regions: $REGIONS"

# The demo Dex is already deployed by demo.sh. Add Headscale as an OIDC client.
python3 - "$ROOT/deploy/demo/dex.yaml" "$HOST" "$HEADSCALE_PORT" <<'PY' > /tmp/poddy-dex-mesh.yaml
import sys
path, host, port = sys.argv[1:4]
s = open(path).read()
needle = "    enablePasswordDB: true\n"
client = f'''      - id: headscale
        name: Headscale
        secret: headscale-secret
        redirectURIs:
          - http://{host}:{port}/oidc/callback

'''
s = s.replace(needle, client + needle, 1).replace("__DEMO_HOST__", host)
sys.stdout.write(s)
PY
kubectl apply -f /tmp/poddy-dex-mesh.yaml >/dev/null
kubectl -n "$NS" rollout restart deploy/dex >/dev/null
kubectl -n "$NS" rollout status deploy/dex --timeout=180s >/dev/null

# Preserve the existing demo values while letting new chart defaults apply.
helm -n "$NS" get values poddy > "${STATE_DIR}/poddy-hub-existing.yaml" 2>/dev/null || true

# Install Headscale without enabling the SSH data plane yet, so we can mint the
# reusable workspace preauth key first.
helm upgrade --install poddy "$ROOT/deploy/helm/poddy" -n "$NS" \
  -f "${STATE_DIR}/poddy-hub-existing.yaml" \
  --set replicaCount=1 \
  --set image.tag="$IMAGE_TAG" --set image.pullPolicy=IfNotPresent \
  --set mesh.enabled=false \
  --set mesh.headscale.enabled=true \
  --set mesh.headscale.serverUrl="http://${HOST}:${HEADSCALE_PORT}" \
  --set mesh.headscale.service.type=NodePort \
  --set mesh.headscale.service.httpNodePort="$HEADSCALE_PORT" \
  --set mesh.headscale.oidc.enabled=true \
  --set mesh.headscale.oidc.issuer="http://${HOST}:30556/dex" \
  --set mesh.headscale.oidc.clientId=headscale \
  --set mesh.headscale.oidc.clientSecret=headscale-secret \
  --set mesh.derp.stunPort="$STUN_PORT" \
  --set mesh.derp.service.stunNodePort="$STUN_PORT" \
  --wait --timeout 10m >/dev/null

kubectl -n "$NS" rollout status deploy/poddy-headscale --timeout=300s >/dev/null
kubectl -n "$NS" exec deploy/poddy-headscale -- headscale users create poddy >/dev/null 2>&1 || true
AUTH_KEY="$(kubectl -n "$NS" exec deploy/poddy-headscale -- \
  headscale preauthkeys create --user poddy --reusable --expiration 720h 2>/dev/null | tail -1 | tr -d '\r\n')"
if [[ -z "$AUTH_KEY" ]]; then
  echo "Could not create Headscale preauth key" >&2
  exit 1
fi

# Enable the SSH mesh on the hub.
helm -n "$NS" get values poddy > "${STATE_DIR}/poddy-hub-existing.yaml"
helm upgrade poddy "$ROOT/deploy/helm/poddy" -n "$NS" \
  -f "${STATE_DIR}/poddy-hub-existing.yaml" \
  --set replicaCount=1 \
  --set mesh.enabled=true \
  --set mesh.controlUrl="http://${HOST}:${HEADSCALE_PORT}" \
  --set mesh.loginUrl="http://${HOST}:${HEADSCALE_PORT}" \
  --set mesh.authKey="$AUTH_KEY" \
  --wait --timeout 10m >/dev/null
kubectl -n "$NS" rollout status deploy/poddy --timeout=300s >/dev/null

REGION_ENTRIES=""
for REGION in $REGIONS; do
  RNS="poddy-${REGION}"
  WNS="poddy-${REGION}-workspaces"
  kubectl create namespace "$RNS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl create namespace "$WNS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

  REGION_KEY="$(openssl rand -hex 32)"
  kubectl -n "$RNS" create secret generic "poddy-${REGION}-region-key" \
    --from-literal=region-key="$REGION_KEY" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

  cat > "${STATE_DIR}/poddy-${REGION}-mesh.yaml" <<YAML
fullnameOverride: poddy-${REGION}
replicaCount: 1
image:
  repository: ghcr.io/slimaineb/poddy
  tag: ${IMAGE_TAG}
  pullPolicy: IfNotPresent
auth:
  token: region-internal
  tokenAdmin: false
regionManager:
  enabled: true
  name: ${REGION}
  hubUrl: http://poddy.${NS}.svc.cluster.local
  existingSecret: poddy-${REGION}-region-key
  keyKey: region-key
workspaceNamespace: ${WNS}
createWorkspaceNamespace: false
defaultTemplate: base
mesh:
  enabled: true
  controlUrl: http://${HOST}:${HEADSCALE_PORT}
  loginUrl: http://${HOST}:${HEADSCALE_PORT}
  authKey: ${AUTH_KEY}
  headscale:
    enabled: false
ssh:
  enabled: true
  publicHost: ${HOST}
  publicPort: 30223
  ca:
    existingSecret: poddy-ssh-ca
http:
  service:
    type: ClusterIP
    port: 80
ingress:
  enabled: false
metrics:
  enabled: false
YAML

  helm upgrade --install "poddy-${REGION}" "$ROOT/deploy/helm/poddy" -n "$RNS" \
    -f "${STATE_DIR}/poddy-${REGION}-mesh.yaml" --wait --timeout 10m >/dev/null
  kubectl -n "$RNS" rollout restart "deploy/poddy-${REGION}" >/dev/null
  kubectl -n "$RNS" rollout status "deploy/poddy-${REGION}" --timeout=300s >/dev/null

  REGION_ENTRIES="${REGION_ENTRIES}  - name: ${REGION}
    displayName: ${REGION}
    key: ${REGION_KEY}
    sshHost: ${HOST}
    sshPort: 30223
"
done

{
  echo "regions:"
  printf '%s' "$REGION_ENTRIES"
} > "${STATE_DIR}/poddy-regions.yaml"
kubectl -n "$NS" create secret generic poddy-regions \
  --from-file=regions.yaml="${STATE_DIR}/poddy-regions.yaml" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null

helm -n "$NS" get values poddy > "${STATE_DIR}/poddy-hub-existing.yaml"
helm upgrade poddy "$ROOT/deploy/helm/poddy" -n "$NS" \
  -f "${STATE_DIR}/poddy-hub-existing.yaml" \
  --set replicaCount=1 \
  --set regionsExistingSecret=poddy-regions \
  --wait --timeout 10m >/dev/null
kubectl -n "$NS" rollout restart deploy/poddy >/dev/null
kubectl -n "$NS" rollout status deploy/poddy --timeout=300s >/dev/null

cat <<MSG

Mesh multi-region demo is ready.

  Hub:        http://${HOST}:30081
  Headscale:  http://${HOST}:${HEADSCALE_PORT}
  Dex:        http://${HOST}:30556/dex
  Regions:    ${REGIONS}
  DERP STUN:  udp/${HOST}:${STUN_PORT}

The workspace SSH dialog shows ws-<workspace> on port 22 for mesh peers.

Remote laptops require HTTPS for the Headscale ingress and embedded DERP; this
HTTP demo validates the in-cluster multi-region mesh. To join a laptop, put
Headscale behind a trusted TLS certificate and run:

  tailscale up --login-server=https://headscale.example.com
MSG
