# Validation multi-région sans peering — 2 clusters kind

Statut : validé le 2026-11-24 sur WSL2/Docker, image
`ghcr.io/slimaineb/poddy:mesh-13cb84f`, chart `0.1.10`.

Objectif : vérifier qu'un hub et une région peuvent vivre dans deux clusters
kind séparés, **sans VPC peering ni transit gateway**, avec :

- le region manager qui ouvre un tunnel WSS sortant vers le hub ;
- la création d'un workspace `region=eu` via l'API du hub ;
- le SSH Desktop/terminal via le mesh Headscale/DERP ;
- le SSH via le tunnel région du portail (chemin Desktop de secours).

Ce document est un runbook de test local, pas une procédure de production. Le
proxy TLS et le CA local sont des artefacts de test à supprimer après usage.

## Topologie testée

```text
hôte 172.29.74.141
  :31081/tcp  -> kind-poddy-hub  NodePort portail
  :31090/tcp  -> kind-poddy-hub  NodePort Headscale (HTTP dans kind)
  :31347/udp  -> kind-poddy-hub  DERP/STUN
  :31443/tcp  -> proxy TLS local -> 127.0.0.1:31090

kind-poddy-hub
  poddy-system      : portail, PostgreSQL, Headscale/DERP
  poddy-workspaces  : templates (le hub ne crée pas de workspace ici)

kind-poddy-eu
  poddy-eu            : region manager
  poddy-eu-workspaces : templates + workspaces

Aucune route pod-à-pod entre les deux clusters.
Region manager : dial sortant vers http://<host>:31081.
Agent workspace : dial sortant vers https://<host>:31443 (Headscale/DERP).
Client Desktop : mesh via <host>:31443 et STUN <host>:31347/udp.
```

Le portail hub n'ouvre aucune connexion vers le cluster région. Le trafic
utilisateur Desktop passe par le mesh ou par le tunnel WSS région → hub.

## Prérequis

- `kind` >= 0.33, Docker, `kubectl`, `helm`, `openssl`, `python3`, Go.
- Images locales : `ghcr.io/slimaineb/poddy:mesh-13cb84f`,
  `postgres:16-alpine`, `ghcr.io/slimaineb/poddy-workspace-base:latest`.
- Ports hôte `31081/tcp`, `31090/tcp`, `31347/udp`, `31443/tcp` libres.
- Un token admin pour l'API du hub.

Variables utilisées dans ce runbook :

```bash
HOST_IP=$(ip -4 route get 1.1.1.1 | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}')
IMAGE_TAG=mesh-13cb84f
TLS=/tmp/poddy-mr-tls
HUB_K=/tmp/poddy-hub.kubeconfig
EU_K=/tmp/poddy-eu.kubeconfig
```

## 1. Créer les deux clusters kind

Le hub mappe les ports hôte nécessaires. La région n'a besoin d'aucun port
entrant.

```bash
cat > /tmp/poddy-kind-hub.yaml <<'EOF'
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    extraPortMappings:
      - containerPort: 31081
        hostPort: 31081
        protocol: TCP
      - containerPort: 31090
        hostPort: 31090
        protocol: TCP
      - containerPort: 31347
        hostPort: 31347
        protocol: UDP
EOF

kind create cluster --name poddy-hub --config /tmp/poddy-kind-hub.yaml --wait 180s
kind export kubeconfig --name poddy-hub --kubeconfig "$HUB_K"

cat > /tmp/poddy-kind-eu.yaml <<'EOF'
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
EOF

kind create cluster --name poddy-eu --config /tmp/poddy-kind-eu.yaml --wait 180s
kind export kubeconfig --name poddy-eu --kubeconfig "$EU_K"

kind load docker-image --name poddy-hub \
  "ghcr.io/slimaineb/poddy:${IMAGE_TAG}" postgres:16-alpine
kind load docker-image --name poddy-eu \
  "ghcr.io/slimaineb/poddy:${IMAGE_TAG}" \
  ghcr.io/slimaineb/poddy-workspace-base:latest
```

## 2. TLS local pour Headscale

Kind n'expose pas directement Headscale en TLS. Pour ce test, un proxy TLS sur
l'hôte termine `https://<host>:31443` et relaie vers le NodePort HTTP `31090`.
Le certificat doit couvrir l'IP de l'hôte, car le client et les agents utilisent
`https://<host>:31443`.

```bash
mkdir -p "$TLS"
openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
  -keyout "$TLS/ca.key" -out "$TLS/ca.crt" \
  -subj "/CN=Poddy multi-region test CA" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes \
  -keyout "$TLS/server.key" -out "$TLS/server.csr" \
  -subj "/CN=${HOST_IP}" >/dev/null 2>&1
cat > "$TLS/server.ext" <<EOF
subjectAltName=IP:${HOST_IP}
extendedKeyUsage=serverAuth
EOF
openssl x509 -req -in "$TLS/server.csr" \
  -CA "$TLS/ca.crt" -CAkey "$TLS/ca.key" -CAcreateserial \
  -out "$TLS/server.crt" -days 825 -sha256 \
  -extfile "$TLS/server.ext" >/dev/null 2>&1

cat > /tmp/poddy-tls-proxy.py <<'PY'
import asyncio, ssl, sys
cert, key, listen_port, backend_host, backend_port = sys.argv[1], sys.argv[2], int(sys.argv[3]), sys.argv[4], int(sys.argv[5])
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain(cert, key)
async def pipe(reader, writer):
    try:
        while True:
            data = await reader.read(65536)
            if not data:
                break
            writer.write(data)
            await writer.drain()
    except Exception:
        pass
    finally:
        try:
            writer.close()
            await writer.wait_closed()
        except Exception:
            pass
async def handle(reader, writer):
    try:
        br, bw = await asyncio.open_connection(backend_host, backend_port)
    except Exception:
        writer.close()
        return
    await asyncio.gather(pipe(reader, bw), pipe(br, writer))
async def main():
    server = await asyncio.start_server(handle, '0.0.0.0', listen_port, ssl=ctx)
    async with server:
        await server.serve_forever()
asyncio.run(main())
PY

nohup python3 /tmp/poddy-tls-proxy.py \
  "$TLS/server.crt" "$TLS/server.key" 31443 127.0.0.1 31090 \
  >/tmp/poddy-tls-proxy.log 2>&1 &
echo $! > /tmp/poddy-tls-proxy.pid
```

Vérifier après le déploiement du hub :

```bash
curl -fsS "http://${HOST_IP}:31081/healthz"
curl -fsS --cacert "$TLS/ca.crt" "https://${HOST_IP}:31443/health"
```

## 3. Déployer le hub

### Secrets

Le CA SSH réutilise ici le CA TLS pour simplifier le test. En production, créer
une CA SSH séparée.

```bash
KUBECONFIG="$HUB_K" kubectl create namespace poddy-system --dry-run=client -o yaml | KUBECONFIG="$HUB_K" kubectl apply -f -
KUBECONFIG="$HUB_K" kubectl create namespace poddy-workspaces --dry-run=client -o yaml | KUBECONFIG="$HUB_K" kubectl apply -f -

TOKEN=$(openssl rand -hex 32)
PGPASS=$(openssl rand -hex 24)
REGION_KEY=$(openssl rand -hex 32)

KUBECONFIG="$HUB_K" kubectl -n poddy-system create secret generic poddy-auth \
  --from-literal=token="$TOKEN" --dry-run=client -o yaml | KUBECONFIG="$HUB_K" kubectl apply -f -
KUBECONFIG="$HUB_K" kubectl -n poddy-system create secret generic poddy-postgres \
  --from-literal=password="$PGPASS" --dry-run=client -o yaml | KUBECONFIG="$HUB_K" kubectl apply -f -
KUBECONFIG="$HUB_K" kubectl -n poddy-system create secret generic poddy-database \
  --from-literal=database-url="postgres://poddy:${PGPASS}@poddy-postgres.poddy-system.svc.cluster.local:5432/poddy?sslmode=disable" \
  --dry-run=client -o yaml | KUBECONFIG="$HUB_K" kubectl apply -f -
KUBECONFIG="$HUB_K" kubectl -n poddy-system create secret generic poddy-ssh-ca \
  --from-file=ssh-ca-key="$TLS/ca.key" --dry-run=client -o yaml | KUBECONFIG="$HUB_K" kubectl apply -f -
KUBECONFIG="$HUB_K" kubectl -n poddy-system create secret generic poddy-ssh-keys \
  --from-literal=authorized_keys="" --dry-run=client -o yaml | KUBECONFIG="$HUB_K" kubectl apply -f -
KUBECONFIG="$HUB_K" kubectl -n poddy-system create secret generic poddy-mesh-ca \
  --from-file=ca.crt="$TLS/ca.crt" --dry-run=client -o yaml | KUBECONFIG="$HUB_K" kubectl apply -f -
```

### PostgreSQL et templates

```bash
KUBECONFIG="$HUB_K" kubectl apply -f deploy/demo/postgres.yaml
KUBECONFIG="$HUB_K" kubectl -n poddy-system rollout status statefulset/poddy-postgres --timeout=300s

helm --kubeconfig "$HUB_K" upgrade --install poddy-templates \
  deploy/helm/poddy-templates -n poddy-workspaces \
  -f deploy/demo/templates-values.yaml --wait --timeout 5m
```

### Chart hub

Points importants :

- `mesh.headscale.host` reste vide : pas d'Ingress, on utilise le proxy TLS.
- `mesh.headscale.serverUrl` et `mesh.controlUrl` pointent vers `:31443`.
- `mesh.derp.stunPort` et son NodePort valent `31347`.
- `regions` déclare la région `eu` avec la clé partagée.

```bash
cat > /tmp/poddy-hub-values.yaml <<EOF
fullnameOverride: poddy
replicaCount: 1
image:
  repository: ghcr.io/slimaineb/poddy
  tag: ${IMAGE_TAG}
  pullPolicy: IfNotPresent
auth:
  existingSecret: poddy-auth
  tokenAdmin: true
database:
  existingSecret: poddy-database
  autoMigrate: true
workspaceNamespace: poddy-workspaces
createWorkspaceNamespace: false
defaultTemplate: base
ssh:
  enabled: true
  ca:
    existingSecret: poddy-ssh-ca
http:
  service:
    type: NodePort
    nodePort: 31081
ingress:
  enabled: false
metrics:
  enabled: false
backup:
  enabled: false
mesh:
  enabled: true
  controlUrl: https://${HOST_IP}:31443
  loginUrl: https://${HOST_IP}:31443
  caSecret: poddy-mesh-ca
  headscale:
    enabled: true
    serverUrl: https://${HOST_IP}:31443
    service:
      type: NodePort
      httpNodePort: 31090
  derp:
    embedded: true
    stunPort: 31347
    service:
      stunNodePort: 31347
regions:
  - name: eu
    displayName: EU
    key: ${REGION_KEY}
    default: true
EOF

helm --kubeconfig "$HUB_K" upgrade --install poddy deploy/helm/poddy \
  -n poddy-system -f /tmp/poddy-hub-values.yaml --wait --timeout 15m
```

Le log du portail doit afficher :

```text
region tunnel connected: eu (1 session(s))
```

## 4. Déployer la région EU

Le region manager lit la clé d'authentification et la clé client depuis le
secret `poddy-mesh` créé par le hub. Il faut aussi installer les templates
**dans le namespace de la région** : le region manager valide le template lors
de la création.

```bash
KUBECONFIG="$EU_K" kubectl create namespace poddy-eu --dry-run=client -o yaml | KUBECONFIG="$EU_K" kubectl apply -f -
KUBECONFIG="$EU_K" kubectl create namespace poddy-eu-workspaces --dry-run=client -o yaml | KUBECONFIG="$EU_K" kubectl apply -f -

KUBECONFIG="$EU_K" kubectl -n poddy-eu create secret generic poddy-eu-region-key \
  --from-literal=region-key="$REGION_KEY" --dry-run=client -o yaml | KUBECONFIG="$EU_K" kubectl apply -f -
KUBECONFIG="$EU_K" kubectl -n poddy-eu create secret generic poddy-ssh-ca \
  --from-file=ssh-ca-key="$TLS/ca.key" --dry-run=client -o yaml | KUBECONFIG="$EU_K" kubectl apply -f -
KUBECONFIG="$EU_K" kubectl -n poddy-eu create secret generic poddy-mesh-ca \
  --from-file=ca.crt="$TLS/ca.crt" --dry-run=client -o yaml | KUBECONFIG="$EU_K" kubectl apply -f -

KUBECONFIG="$HUB_K" kubectl -n poddy-system get secret poddy-mesh -o json > /tmp/poddy-mesh.json
python3 - <<'PY'
import json, base64, pathlib
data = json.load(open('/tmp/poddy-mesh.json'))['data']
for key in ('auth-key', 'client-auth-key'):
    pathlib.Path('/tmp/poddy-' + key).write_text(base64.b64decode(data[key]).decode().strip())
PY
AUTH_KEY=$(cat /tmp/poddy-auth-key)
CLIENT_KEY=$(cat /tmp/poddy-client-auth-key)

helm --kubeconfig "$EU_K" upgrade --install poddy-templates \
  deploy/helm/poddy-templates -n poddy-eu-workspaces \
  -f deploy/demo/templates-values.yaml --wait --timeout 5m

cat > /tmp/poddy-eu-values.yaml <<EOF
fullnameOverride: poddy-eu
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
  name: eu
  hubUrl: http://${HOST_IP}:31081
  existingSecret: poddy-eu-region-key
  keyKey: region-key
workspaceNamespace: poddy-eu-workspaces
createWorkspaceNamespace: false
defaultTemplate: base
ssh:
  enabled: true
  publicHost: ${HOST_IP}
  publicPort: 2222
  ca:
    existingSecret: poddy-ssh-ca
mesh:
  enabled: true
  controlUrl: https://${HOST_IP}:31443
  loginUrl: https://${HOST_IP}:31443
  authKey: ${AUTH_KEY}
  clientAuthKey: ${CLIENT_KEY}
  caSecret: poddy-mesh-ca
  headscale:
    enabled: false
http:
  service:
    type: ClusterIP
ingress:
  enabled: false
metrics:
  enabled: false
backup:
  enabled: false
EOF

helm --kubeconfig "$EU_K" upgrade --install poddy-eu deploy/helm/poddy \
  -n poddy-eu -f /tmp/poddy-eu-values.yaml --wait --timeout 15m
```

Vérifier :

```bash
KUBECONFIG="$EU_K" kubectl -n poddy-eu logs deploy/poddy-eu --tail=40
```

Le log doit contenir :

```text
region tunnel connected to ws://<host>:31081/api/internal/region/tunnel as eu
templates: loaded 5 templates from 5 ConfigMaps
```

## 5. Créer un workspace dans la région

```bash
curl -fsS -H "Authorization: Bearer ${TOKEN}" \
  "http://${HOST_IP}:31081/api/regions"

curl -fsS -X POST "http://${HOST_IP}:31081/api/workspaces" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{"name":"mr-eu-1","template":"base","region":"eu","parameters":{"cpu":"250m","memory":"512Mi","storage":"500Mi"}}'
```

Avec le token admin, le workspace créé s'appelle `token-mr-eu-1` (préfixe
`token-`). Vérifier qu'il est bien dans la région et sur le nœud EU :

```bash
curl -fsS -H "Authorization: Bearer ${TOKEN}" \
  "http://${HOST_IP}:31081/api/workspaces/token-mr-eu-1"

KUBECONFIG="$EU_K" kubectl -n poddy-eu-workspaces get pods,sts,pvc -o wide
```

Attendu : `phase=Running`, `region=eu`, `nodeName=poddy-eu-control-plane`.

## 6. Vérifier le SSH mesh et le chemin Desktop

Construire le helper et signer un certificat SSH avec l'extension de workspace :

```bash
go build -o /tmp/poddy-connect ./cmd/poddy-connect
ssh-keygen -q -t ed25519 -N '' -f /tmp/poddy-user
ssh-keygen -q -s "$TLS/ca.key" -I mr-eu-test -n dev \
  -V -5m:+1h -O clear -O permit-pty \
  -O extension:poddy-workspace=token-mr-eu-1 /tmp/poddy-user.pub
```

SSH via mesh :

```bash
ssh \
  -o "ProxyCommand=/tmp/poddy-connect --mode mesh \
      --control-url https://${HOST_IP}:31443 \
      --auth-key-file /tmp/poddy-client-auth-key \
      --workspace token-mr-eu-1 \
      --ca-file $TLS/ca.crt \
      --state-dir /tmp/poddy-client-state" \
  -o CertificateFile=/tmp/poddy-user-cert.pub \
  -i /tmp/poddy-user -o IdentitiesOnly=yes \
  -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
  dev@token-mr-eu-1 'hostname; echo PODDY_MESH_OK'
```

SSH via le chemin Desktop/portail (le ticket est dans l'URL renvoyée par
`POST /desktop`) :

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer ${TOKEN}" \
  "http://${HOST_IP}:31081/api/workspaces/token-mr-eu-1/desktop" \
  > /tmp/poddy-desktop.json

python3 - <<'PY'
import json, base64, urllib.parse
url = json.load(open('/tmp/poddy-desktop.json'))['url']
payload = dict(urllib.parse.parse_qsl(urllib.parse.urlparse(url).query))['payload']
data = json.loads(base64.urlsafe_b64decode(payload + '=' * (-len(payload) % 4)))
open('/tmp/poddy-desktop-ticket', 'w').write(data.pop('ticket'))
print(json.dumps(data, indent=2))
PY

ssh \
  -o "ProxyCommand=/tmp/poddy-connect --portal http://${HOST_IP}:31081 \
      --workspace token-mr-eu-1 --ticket-file /tmp/poddy-desktop-ticket" \
  -o CertificateFile=/tmp/poddy-user-cert.pub \
  -i /tmp/poddy-user -o IdentitiesOnly=yes \
  -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
  dev@token-mr-eu-1 'hostname; echo PODDY_PORTAL_REGION_SSH_OK'
```

Le payload Desktop attendu contient :

```json
{
  "transport": "mesh",
  "workspace": "token-mr-eu-1",
  "sshHost": "ws-token-mr-eu-1",
  "mesh": true
}
```

## 7. Vérifier l'absence de chemin direct entre clusters

Depuis le workspace EU, l'IP du pod hub ne doit pas être joignable directement.
Utiliser l'IP du pod hub (pas l'IP de Service, les CIDR de Service des deux
clusters se chevauchent) :

```bash
HUB_POD_IP=$(KUBECONFIG="$HUB_K" kubectl -n poddy-system get pod \
  -l app.kubernetes.io/name=poddy-portal \
  -o jsonpath='{.items[0].status.podIP}')

KUBECONFIG="$EU_K" kubectl -n poddy-eu-workspaces exec \
  poddy-token-mr-eu-1-0 -c workspace -- sh -c \
  "curl -m 3 -sS -o /dev/null -w 'http=%{http_code}\\n' http://${HUB_POD_IP}:8080/healthz"
```

Attendu : timeout. Le tunnel région et le mesh passent par les ports hôte, pas
par une route pod-à-pod.

## 8. Nettoyage

```bash
kill "$(cat /tmp/poddy-tls-proxy.pid)" 2>/dev/null || true
kind delete cluster --name poddy-hub
kind delete cluster --name poddy-eu
rm -rf "$TLS" \
  /tmp/poddy-tls-proxy.py /tmp/poddy-tls-proxy.log /tmp/poddy-tls-proxy.pid \
  /tmp/poddy-kind-hub.yaml /tmp/poddy-kind-eu.yaml \
  /tmp/poddy-hub-values.yaml /tmp/poddy-eu-values.yaml \
  /tmp/poddy-hub.kubeconfig /tmp/poddy-eu.kubeconfig \
  /tmp/poddy-auth-key /tmp/poddy-client-auth-key /tmp/poddy-mesh.json \
  /tmp/poddy-desktop.json /tmp/poddy-desktop-ticket \
  /tmp/poddy-user /tmp/poddy-user.pub /tmp/poddy-user-cert.pub \
  /tmp/poddy-connect /tmp/poddy-client-state
```

Supprimer aussi les secrets, clés SSH, tickets et CA de test. Ne jamais
committer ces artefacts. `kind create` peut changer le contexte kubectl
courant ; restaurer le contexte initial si nécessaire.

## Résultats observés

- La région `eu` est enregistrée côté hub avec `available=true`,
  `workspaces=1`, `running=1`.
- Le workspace `token-mr-eu-1` tourne dans `poddy-eu-workspaces` sur
  `poddy-eu-control-plane`.
- Le SSH mesh depuis l'hôte renvoie `PODDY_MESH_OK`.
- Le SSH via le tunnel région du portail renvoie `PODDY_PORTAL_REGION_SSH_OK`.
- Le payload Desktop indique `transport=mesh` et
  `sshHost=ws-token-mr-eu-1`.
- Le pod EU ne joint pas directement le pod hub ; aucun peering n'est utilisé.

## Points d'attention

- **Templates par région** : sans `poddy-templates` dans le namespace de la
  région, la création échoue avec `unknown template "base"`. Le hub et chaque
  région doivent avoir les templates.
- **Nom du workspace** : avec le token admin, le nom effectif est préfixé par
  `token-`. Utiliser le nom renvoyé par l'API, pas seulement le nom demandé.
- **`sshHost` de région** : l'enregistrement affiche `127.0.0.1:2222` par
  défaut. Sans impact en mesh, mais à configurer si un accès SSH direct région
  est utilisé.
- **Watchdog mesh** : pendant le join initial, le watchdog a recréé une fois le
  pod workspace car l'agent n'était pas prêt. Après recreation, l'agent a
  rejoint le mesh et le SSH a fonctionné. Surveiller si cela se reproduit.
- **CA SSH partagé** : le hub et toutes les régions doivent utiliser le même
  secret CA SSH, sinon les certificats émis par le hub sont refusés par les
  workspaces régionaux.
- **Proxy TLS local** : solution de test uniquement. En production, terminer
  TLS sur un ingress/load balancer avec un certificat valide.
- **Hub mono-replica** : le tunnel région est possédé par un pod hub ; ce test
  ne valide pas la HA du hub avec plusieurs replicas.
