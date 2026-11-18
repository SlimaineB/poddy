#!/usr/bin/env bash
set -Eeuo pipefail

NAME="${1:?cluster name required}"
KUBECONFIG_PATH="${2:?kubeconfig path required}"
PORTAL_PORT="${3:?portal local port required}"
SSH_PORT="${4:?ssh local port required}"
ROOT="${ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
WORKDIR="${WORKDIR:-/tmp/poddy-e2e-${NAME}}"
PORTAL_IMAGE_REPO="${PORTAL_IMAGE_REPO:-ghcr.io/slimaineb/poddy}"
PORTAL_IMAGE_TAG="${PORTAL_IMAGE_TAG:-latest}"
WS="test-${NAME}"
WS_NAME="$WS"
NS="poddy-system"
WS_NS="poddy-workspaces"
KUBECTL=(kubectl --kubeconfig "$KUBECONFIG_PATH")
HELM=(helm --kubeconfig "$KUBECONFIG_PATH")
PIDS=()

mkdir -p "$WORKDIR"
rm -f "$WORKDIR"/*.log "$WORKDIR"/*.json "$WORKDIR"/*.txt "$WORKDIR"/ssh_config 2>/dev/null || true

cleanup() {
  local pid
  for pid in "${PIDS[@]:-}"; do
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  done
}
trap cleanup EXIT

fail() {
  echo "FAIL [$NAME]: $*" >&2
  "${KUBECTL[@]}" get pods -A -o wide >"$WORKDIR/pods.txt" 2>&1 || true
  "${KUBECTL[@]}" get events -A --sort-by=.lastTimestamp >"$WORKDIR/events.txt" 2>&1 || true
  "${KUBECTL[@]}" -n "$NS" logs deploy/poddy --tail=200 >"$WORKDIR/portal.log" 2>&1 || true
  "${KUBECTL[@]}" -n "$NS" logs deploy/poddy-bastion --tail=200 >"$WORKDIR/bastion.log" 2>&1 || true
  "${KUBECTL[@]}" -n "$WS_NS" logs "poddy-${WS_NAME}-0" -c workspace --tail=200 >"$WORKDIR/workspace.log" 2>&1 || true
  echo "Diagnostics: $WORKDIR" >&2
  exit 1
}

echo "==> [$NAME] source $ROOT ($(git -C "$ROOT" rev-parse --short HEAD))"
"${KUBECTL[@]}" version --output=yaml >"$WORKDIR/version.txt"
"${KUBECTL[@]}" get nodes -o wide | tee "$WORKDIR/nodes.txt"

KEY="$WORKDIR/id_ed25519"
rm -f "$KEY" "$KEY.pub"
ssh-keygen -q -t ed25519 -N "" -C "user:token" -f "$KEY"
PUB="$(cat "$KEY.pub")"

echo "==> [$NAME] namespaces and secrets"
"${KUBECTL[@]}" create namespace "$NS" --dry-run=client -o yaml | "${KUBECTL[@]}" apply -f - >/dev/null
"${KUBECTL[@]}" create namespace "$WS_NS" --dry-run=client -o yaml | "${KUBECTL[@]}" apply -f - >/dev/null

if ! "${KUBECTL[@]}" -n "$NS" get secret poddy-auth >/dev/null 2>&1; then
  TOKEN="$(openssl rand -hex 32)"
  "${KUBECTL[@]}" -n "$NS" create secret generic poddy-auth \
    --from-literal=token="$TOKEN" --dry-run=client -o yaml | "${KUBECTL[@]}" apply -f - >/dev/null
fi
TOKEN="$("${KUBECTL[@]}" -n "$NS" get secret poddy-auth -o jsonpath='{.data.token}' | base64 -d)"

if ! "${KUBECTL[@]}" -n "$NS" get secret poddy-postgres >/dev/null 2>&1; then
  PGPASSWORD="$(openssl rand -hex 16)"
  "${KUBECTL[@]}" -n "$NS" create secret generic poddy-postgres \
    --from-literal=password="$PGPASSWORD" --dry-run=client -o yaml | "${KUBECTL[@]}" apply -f - >/dev/null
  "${KUBECTL[@]}" -n "$NS" create secret generic poddy-database \
    --from-literal=database-url="postgres://poddy:${PGPASSWORD}@poddy-postgres:5432/poddy?sslmode=disable" \
    --dry-run=client -o yaml | "${KUBECTL[@]}" apply -f - >/dev/null
fi

echo "==> [$NAME] PostgreSQL"
"${KUBECTL[@]}" apply -f "$ROOT/deploy/demo/postgres.yaml" >/dev/null
"${KUBECTL[@]}" -n "$NS" rollout status statefulset/poddy-postgres --timeout=300s

echo "==> [$NAME] templates chart from GitHub source"
"${HELM[@]}" upgrade --install poddy-templates "$ROOT/deploy/helm/poddy-templates" \
  -n "$WS_NS" --wait --timeout 5m >"$WORKDIR/templates-helm.txt"

echo "==> [$NAME] portal chart from GitHub source"
"${HELM[@]}" upgrade --install poddy "$ROOT/deploy/helm/poddy" \
  -n "$NS" \
  --set image.repository="$PORTAL_IMAGE_REPO" \
  --set image.tag="$PORTAL_IMAGE_TAG" \
  --set image.pullPolicy=IfNotPresent \
  --set auth.existingSecret=poddy-auth \
  --set auth.tokenAdmin=true \
  --set database.existingSecret=poddy-database \
  --set database.autoMigrate=true \
  --set ssh.enabled=true \
  --set ssh.requireAgent=false \
  --set ssh.agent.enabled=false \
  --set ssh.service.type=ClusterIP \
  --set ssh.publicHost=127.0.0.1 \
  --set ssh.publicPort=2222 \
  --set-string "ssh.authorizedKeys[0]=$PUB" \
  --set workspaceNamespace="$WS_NS" \
  --set createWorkspaceNamespace=false \
  --wait --timeout 10m >"$WORKDIR/portal-helm.txt"

"${KUBECTL[@]}" -n "$NS" rollout status deploy/poddy --timeout=300s
"${KUBECTL[@]}" -n "$NS" rollout status deploy/poddy-bastion --timeout=300s

echo "==> [$NAME] port-forwards"
"${KUBECTL[@]}" -n "$NS" port-forward svc/poddy "${PORTAL_PORT}:80" >"$WORKDIR/portal-pf.log" 2>&1 &
PIDS+=("$!")
"${KUBECTL[@]}" -n "$NS" port-forward svc/poddy-ssh "${SSH_PORT}:2222" >"$WORKDIR/ssh-pf.log" 2>&1 &
PIDS+=("$!")

for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:${PORTAL_PORT}/healthz" >"$WORKDIR/healthz.txt" 2>/dev/null; then
    break
  fi
  sleep 1
done
grep -qi 'ok' "$WORKDIR/healthz.txt" || fail "portal healthz did not become ready"
echo "portal healthz: $(cat "$WORKDIR/healthz.txt")"

echo "==> [$NAME] create workspace $WS"
PAYLOAD="$(jq -nc --arg name "$WS" '{name:$name,template:"base",parameters:{cpu:"250m",memory:"512Mi",storage:"500Mi",browserVSCode:"false"}}')"
CREATE_CODE="$(curl -sS -o "$WORKDIR/create.json" -w '%{http_code}' -X POST \
  "http://127.0.0.1:${PORTAL_PORT}/api/workspaces" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H 'Content-Type: application/json' \
  -d "$PAYLOAD")"
cat "$WORKDIR/create.json"
[[ "$CREATE_CODE" == "201" ]] || fail "workspace create HTTP ${CREATE_CODE}"
WS_NAME="$(jq -r '.name // empty' "$WORKDIR/create.json")"
[[ -n "$WS_NAME" ]] || fail "workspace create response missing name"

phase=""
for _ in $(seq 1 180); do
  curl -fsS "http://127.0.0.1:${PORTAL_PORT}/api/workspaces/${WS_NAME}" \
    -H "Authorization: Bearer ${TOKEN}" >"$WORKDIR/workspace.json" 2>/dev/null || true
  phase="$(jq -r '.phase // empty' "$WORKDIR/workspace.json" 2>/dev/null || true)"
  if [[ "$phase" == "Running" ]]; then
    break
  fi
  if [[ "$phase" == "Failed" ]]; then
    fail "workspace entered Failed phase"
  fi
  sleep 2
done
[[ "$phase" == "Running" ]] || fail "workspace did not reach Running (phase=${phase:-unknown})"
echo "workspace phase: $phase"

"${KUBECTL[@]}" -n "$WS_NS" get pod "poddy-${WS_NAME}-0" -o wide | tee "$WORKDIR/workspace-pod.txt"
"${KUBECTL[@]}" -n "$WS_NS" get pvc "poddy-${WS_NAME}" -o wide | tee "$WORKDIR/workspace-pvc.txt"
PVC_PHASE="$("${KUBECTL[@]}" -n "$WS_NS" get pvc "poddy-${WS_NAME}" -o jsonpath='{.status.phase}')"
[[ "$PVC_PHASE" == "Bound" ]] || fail "workspace PVC is ${PVC_PHASE:-unknown}"
IMAGE="$("${KUBECTL[@]}" -n "$WS_NS" get pod "poddy-${WS_NAME}-0" -o jsonpath='{.spec.containers[?(@.name=="workspace")].image}')"
echo "workspace image: $IMAGE"

echo "==> [$NAME] SSH through bastion ($WS_NAME)"
cat >"$WORKDIR/ssh_config" <<EOF
Host poddy-ssh-${WS_NAME}
    HostName poddy-ssh-${WS_NAME}
    User dev
    ProxyCommand ssh -p ${SSH_PORT} -i ${KEY} -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=${WORKDIR}/known_hosts -W %h:%p poddy@127.0.0.1
    IdentityFile ${KEY}
    IdentitiesOnly yes
    StrictHostKeyChecking accept-new
    UserKnownHostsFile ${WORKDIR}/known_hosts
    LogLevel ERROR
EOF

for _ in $(seq 1 30); do
  if timeout 3 bash -c "</dev/tcp/127.0.0.1/${SSH_PORT}" 2>/dev/null; then
    break
  fi
  sleep 1
done

SSH_OUT="$(ssh -F "$WORKDIR/ssh_config" -o BatchMode=yes -o ConnectTimeout=20 \
  "poddy-ssh-${WS_NAME}" 'printf "SSH_OK user=%s cwd=%s\n" "$(whoami)" "$(pwd)"' 2>"$WORKDIR/ssh.err")" \
  || { cat "$WORKDIR/ssh.err" >&2; fail "SSH command failed"; }
echo "$SSH_OUT" | tee "$WORKDIR/ssh.txt"
grep -q 'SSH_OK user=dev cwd=/home/dev' "$WORKDIR/ssh.txt" || fail "unexpected SSH result: $SSH_OUT"

echo "==> [$NAME] PASS"
"${KUBECTL[@]}" -n "$NS" get deploy,pods,svc | tee "$WORKDIR/summary.txt"
"${KUBECTL[@]}" -n "$WS_NS" get statefulset,pods,pvc | tee -a "$WORKDIR/summary.txt"
