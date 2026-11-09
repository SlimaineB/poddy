# Regions

A multi-region deployment uses one central hub and one region manager per
Kubernetes cluster. The hub owns authentication, RBAC, users, templates, the
workspace list, the UI, and external auth tokens. Each region manager owns the
Kubernetes API connection, workspace pods, PVCs, stopped-state ConfigMap,
terminal and VS Code proxying, and the region SSH bastion.

```text
Browser ──► central hub ── signed identity ──► region manager (eu) ──► pods
                       └─ signed identity ──► region manager (us) ──► pods
```

The hub sends `X-Poddy-Region-Identity`, an HMAC-signed short-lived identity
(2 minutes) containing the user and admin flag. Region managers reject every
untrusted request, including direct browser requests without the signature.
Workspace operations, logs, terminal WebSocket, browser VS Code, SSH details,
desktop tickets, and external-auth workspace callbacks are routed by the hub.
SSH public keys are replicated from the hub registry to every region.

## What lives where

| Component | Central hub | Region manager |
|-----------|-------------|----------------|
| OIDC login, sessions, RBAC, teams, users | Yes | No |
| Template catalog and access rules | Yes | No |
| Workspace list and UI | Yes | No |
| Workspace pods, PVCs, stopped state | No | Yes |
| SSH bastion and SSH endpoint | No | Yes |
| PostgreSQL metadata mirror and audit | Yes | No |
| Region key and signed identity | Verifies region keys | Trusts hub signature |

Region managers do not need OIDC. They only accept requests signed by the hub.
Do not expose a region manager portal Service publicly; the hub proxies all
user traffic. Use a private network or NetworkPolicy between the hub and region
managers.

## Add a region

The example adds a region named `eu` in its own Kubernetes cluster or namespace.
Use a unique region name, a unique shared key, and a dedicated workspace
namespace.

### 1. Create namespaces

```bash
kubectl create namespace poddy-eu
kubectl create namespace poddy-eu-workspaces
```

The region manager release lives in `poddy-eu`; workspaces live in
`poddy-eu-workspaces`. Set `createWorkspaceNamespace: false` and create the
workspace namespace yourself so `helm uninstall` cannot delete it.

### 2. Generate the shared region key

The same key must exist in the hub region entry and in the region manager
Secret. Generate one key per region:

```bash
REGION_KEY="$(openssl rand -hex 32)"

kubectl -n poddy-eu create secret generic poddy-eu-region-key \
  --from-literal=region-key="$REGION_KEY"
```

Keep `$REGION_KEY` for the hub step. Do not reuse a key across regions.

### 3. Create a stable SSH host key Secret

A stable host key avoids `known_hosts` warnings after region restarts:

```bash
ssh-keygen -t ed25519 -N '' -f /tmp/poddy-eu-host-key -C poddy-eu

kubectl -n poddy-eu create secret generic poddy-eu-host-key \
  --from-file=ssh_host_ed25519_key=/tmp/poddy-eu-host-key
```

### 4. Create the region manager values

Save as `values-eu.yaml`. Replace the image tag, hub URL, SSH host, and ports
with your values.

```yaml
fullnameOverride: poddy-eu
replicaCount: 1

image:
  repository: ghcr.io/slimaineb/poddy
  tag: sha-269a643
  pullPolicy: IfNotPresent

bastion:
  enabled: true
  replicaCount: 1

# Region managers do not use this token for user login. The chart still
# requires an auth token or an existing secret when OIDC is disabled.
auth:
  token: region-internal
  tokenAdmin: false

regionManager:
  enabled: true
  name: eu
  # Reachable from the region manager. Use the public hub URL when the hub is
  # in another cluster.
  hubUrl: http://poddy.poddy-system.svc.cluster.local
  existingSecret: poddy-eu-region-key
  keyKey: region-key

workspaceNamespace: poddy-eu-workspaces
createWorkspaceNamespace: false

rbac:
  create: true
  createConfigMap: false
  # Required by workspace schedules in chart 0.1.0, which updates the
  # schedule annotation on the workspace Pod. Remove this once the chart Role
  # includes pods update/patch.
  extraRules:
    - apiGroups: [""]
      resources: ["pods"]
      verbs: ["update", "patch"]

defaultTemplate: base

ssh:
  enabled: true
  addr: ":2222"
  # Public endpoint shown to users in the UI and SSH instructions.
  publicHost: 172.29.74.141
  publicPort: 30224
  existingHostKeySecret: poddy-eu-host-key
  hostKeySecretKey: ssh_host_ed25519_key
  allowUnmappedKeys: false
  service:
    type: NodePort
    port: 2222
    nodePort: 30224

# The hub reaches the region manager through its ClusterIP Service.
http:
  service:
    type: ClusterIP
    port: 80

ingress:
  enabled: false

metrics:
  enabled: false
```

`sshHost` and `sshPort` on the hub must point to the endpoint users can reach,
not to the internal `http` Service. On a cloud load balancer, use the region
bastion's external hostname and port. On k3s, use the node IP and the SSH
NodePort.

### 5. Install the region manager

```bash
helm upgrade --install poddy-eu oci://ghcr.io/slimaineb/charts/poddy \
  --version 0.1.0 \
  -n poddy-eu \
  -f values-eu.yaml \
  --wait
```

The region manager Service is named `poddy-eu` by default when
`fullnameOverride: poddy-eu` is set. The hub will call
`http://poddy-eu.poddy-eu.svc.cluster.local` in the same cluster.

### 6. Register the region on the hub

The hub reads a `regions.yaml` file from a Secret. The recommended production
setup uses `regionsExistingSecret`:

```bash
kubectl -n poddy-system create secret generic poddy-regions \
  --from-file=regions.yaml=regions.yaml
```

`regions.yaml`:

```yaml
regions:
  - name: eu
    displayName: Europe
    # Hub -> region manager, internal and private.
    url: http://poddy-eu.poddy-eu.svc.cluster.local
    # Must match regionManager.key in the eu region.
    key: <REGION_KEY>
    # User -> region bastion, public endpoint.
    sshHost: 172.29.74.141
    sshPort: 30224
    lat: 50.1109
    lon: 8.6821
    default: true
```

`lat` and `lon` are optional coordinates for the World map view. Regions
without them fall back to coordinates based on the region name, then to
`0, 0`. Set `default: true` on exactly one region; it is used when the UI or API
does not specify a region.

Hub values:

```yaml
regionsExistingSecret: poddy-regions
regionsKey: regions.yaml
```

If the hub also hosts local workspaces (no regions), add the same
`rbac.extraRules` shown in the region values so schedules can update Pod
annotations.

If you manage regions directly in Helm values instead, set `regions` and let
the chart create the Secret:

```yaml
regions:
  - name: eu
    displayName: Europe
    url: http://poddy-eu.poddy-eu.svc.cluster.local
    key: <REGION_KEY>
    sshHost: 172.29.74.141
    sshPort: 30224
    lat: 50.1109
    lon: 8.6821
    default: true
```

### 7. Upgrade the hub

```bash
helm upgrade --install poddy oci://ghcr.io/slimaineb/charts/poddy \
  --version 0.1.0 \
  -n poddy-system \
  -f values-hub.yaml \
  --wait
```

When using `regions` values, the chart adds a checksum annotation and the hub
rolls automatically. When using `regionsExistingSecret`, the Secret is mounted
as a file and is not checksummed; restart the hub after changing it:

```bash
kubectl -n poddy-system rollout restart deployment/poddy
```

### 8. Verify

```bash
kubectl -n poddy-eu get pods
kubectl -n poddy-system logs deployment/poddy --tail=50
```

The hub logs `central hub configured with N regions`. Check the API:

```bash
curl -fsS http://<hub>/api/regions
```

The new region must show `"available": true`. In the UI, the region filter and
World map should list it. Create a workspace with that region selected and
confirm the pod appears in the region workspace namespace.

## Add a second region

Repeat the steps with a new name, namespace, key, SSH endpoint, and workspace
namespace. Keep the region names unique. For example, a local k3s deployment
can simulate two regions on one node:

| Region | Release | Region namespace | Workspace namespace | SSH endpoint |
|--------|---------|------------------|---------------------|--------------|
| `eu` | `poddy-eu` | `poddy-eu` | `poddy-eu-workspaces` | `172.29.74.141:30224` |
| `us` | `poddy-us` | `poddy-us` | `poddy-us-workspaces` | `172.29.74.141:30225` |

The hub remains exposed on its own NodePort or ingress, for example
`http://172.29.74.141:30081`. Region portal Services stay `ClusterIP`; only the
region SSH bastion uses a NodePort.

## Rotate or remove a region

To rotate a region key:

1. Generate a new key and update the region Secret.
2. Update the hub `regions.yaml` entry with the same key.
3. Restart the region Deployment and the hub Deployment.

To remove a region, delete its entry from `regions.yaml`, restart the hub, then
uninstall the region release. Delete the region workspace namespace only after
you have deleted or migrated its workspaces. There is no automatic failover of
an existing workspace between regions; cross-region placement is explicit at
creation time.

## Troubleshooting

| Symptom | Check |
|---------|-------|
| `"available": false` | Region Service name, `url` in `regions.yaml`, NetworkPolicy, and hub logs. |
| `502` for a workspace | The owning region is unreachable or the region key does not match. |
| `403` from a region | `regionManager.key` and the hub region `key` differ, or the request did not come through the hub. |
| SSH connects to the wrong host | `sshHost`/`sshPort` on the hub must be the public bastion endpoint, not the internal portal Service. |
| Hub does not see a new region | Restart the hub after editing `regionsExistingSecret`; values-based `regions` roll automatically. |
| Region workspaces missing from PostgreSQL | The PostgreSQL mirror is configured on the hub only. Region managers own their workspace objects; use the hub API or region namespace for live state. |
