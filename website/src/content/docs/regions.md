---
title: "Regions"
description: "Run a central hub with isolated region managers connected by outbound tunnels."
---

A multi-region deployment uses one central hub and one region manager per
Kubernetes cluster. The hub owns authentication, RBAC, users, templates, the
workspace list, the UI, and external auth tokens. Each region manager owns the
Kubernetes API connection, workspace StatefulSets, PVCs, legacy stopped-state ConfigMap,
terminal and desktop IDE proxying, and the outbound region tunnel.

```text
Browser ──► central hub ── signed identity ──► region tunnel (eu) ──► region manager ──► pods
                       └─ signed identity ──► region tunnel (us) ──► region manager ──► pods

region manager ── outbound WSS ──► central hub
```

Each region manager opens an outbound WebSocket tunnel to the hub. The hub
routes workspace HTTP and WebSocket traffic through that tunnel, so it does not
need inbound access to the region cluster and no VPN peering is required. The
hub sends `X-Poddy-Region-Identity`, an HMAC-signed short-lived identity
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
| SSH endpoint | Yes (in the portal) | No |
| PostgreSQL control plane and audit | Yes | No (uses hub) |
| Access grants, presence, and quotas | Yes (PostgreSQL) | Proxied to hub |
| Region key and signed identity | Verifies region keys | Trusts hub signature |
| Outbound tunnel to hub | Accepts | Opens |

Region managers do not need OIDC. They only accept requests signed by the hub.
Do not expose a region manager portal Service publicly; the hub proxies all
user traffic through the outbound tunnel.

### Network requirements

- Hub ingress: HTTPS/WSS `443` from users and from region managers. The ingress
  must forward WebSocket upgrades for `/api/internal/region/tunnel`.
- Region manager egress: HTTPS/WSS to `regionManager.hubUrl`.
- No hub-to-region inbound path and no VPN peering are required.
- No region SSH endpoint, load balancer, peering, or inbound firewall rule is
  required. Users connect to the hub SSH endpoint for every workspace.

Tunnel sessions live in the hub process. With multiple hub replicas, each owner
publishes a Kubernetes Lease and other replicas forward SSH and API streams to
it over the internal agent port, so no sticky routing is required.

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

### 3. Create the region manager values

Save as `values-eu.yaml`. Replace the image tag, hub URL, SSH host, and ports
with your values.

```yaml
fullnameOverride: poddy-eu
replicaCount: 1

image:
  repository: ghcr.io/slimaineb/poddy
  tag: sha-269a643
  pullPolicy: IfNotPresent

# Region managers do not use this token for user login. The chart still
# requires an auth token or an existing secret when OIDC is disabled.
auth:
  token: region-internal
  tokenAdmin: false

regionManager:
  enabled: true
  name: eu
  # Public hub URL. The region manager opens an outbound WSS tunnel to it.
  hubUrl: https://poddy.example.com
  existingSecret: poddy-eu-region-key
  keyKey: region-key

workspaceNamespace: poddy-eu-workspaces
createWorkspaceNamespace: false

rbac:
  create: true
  createConfigMap: false
  # Required by workspace schedules in current charts, which update the
  # schedule annotation on the workspace Pod. Remove this once the chart Role
  # includes pods update/patch.
  extraRules:
    - apiGroups: [""]
      resources: ["pods"]
      verbs: ["update", "patch"]

defaultTemplate: base

ssh:
  # The hub terminates SSH for every workspace. Keep this enabled in regions so
  # the shared CA is mounted and workspace authorized_keys trusts hub-issued
  # certificates. The chart does not create an SSH Service or listener in a
  # region manager.
  enabled: true
  publicHost: ssh.hub.example.com
  publicPort: 2222
  ca:
    existingSecret: poddy-ssh-ca

# The hub uses the outbound tunnel. This Service is local to the region.
http:
  service:
    type: ClusterIP
    port: 80

ingress:
  enabled: false

metrics:
  enabled: false
```

`hubUrl` must be reachable from the region manager over outbound HTTPS/WSS. In
the same cluster it can be the hub ClusterIP Service; across clusters use the
public hub URL. `sshHost` and `sshPort` on the hub must point to the endpoint
users can reach, not to the internal `http` Service. On k3s, use the node IP
and the hub SSH NodePort. Set `replicaCount > 1` for region-manager HA; each
replica opens its own tunnel and the hub load-balances requests across them.

### 5. Install the region manager

```bash
helm upgrade --install poddy-eu oci://ghcr.io/slimaineb/charts/poddy \
  --version 0.1.10 \
  -n poddy-eu \
  -f values-eu.yaml \
  --wait
```

The region manager Service is named `poddy-eu` by default when
`fullnameOverride: poddy-eu` is set. It does not need to be reachable from the
hub; the region manager connects outbound to `regionManager.hubUrl`.

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
    # Must match regionManager.key in the eu region.
    key: <REGION_KEY>
    # Legacy display fields; SSH always uses the hub endpoint.
    # Keep them pointed at the hub, not at a region-specific port.
    sshHost: 172.29.74.141
    sshPort: 30223
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
database:
  existingSecret: poddy-database
  urlKey: database-url
  autoMigrate: true

regionsExistingSecret: poddy-regions
regionsKey: regions.yaml
```

The hub needs PostgreSQL; create `poddy-database` in the hub namespace. Region
managers do not need database credentials and use the hub control plane.

If the hub also hosts local workspaces (no regions), add the same
`rbac.extraRules` shown in the region values so schedules can update Pod
annotations.

If you manage regions directly in Helm values instead, set `regions` and let
the chart create the Secret:

```yaml
regions:
  - name: eu
    displayName: Europe
    key: <REGION_KEY>
    sshHost: 172.29.74.141
    sshPort: 30223
    lat: 50.1109
    lon: 8.6821
    default: true
```

`url` is optional. Omit it for tunnel-only routing. If set, the hub falls back
to direct HTTP when the tunnel is down; that fallback still requires a network
path from the hub to the region.

### 7. Upgrade the hub

```bash
helm upgrade --install poddy oci://ghcr.io/slimaineb/charts/poddy \
  --version 0.1.10 \
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

The hub logs `central hub configured with N regions` and
`region tunnel connected: eu` when the region connects. Check the API:

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
`http://172.29.74.141:30081`. Region portal Services stay `ClusterIP`; the hub
SSH NodePort or load balancer is the only SSH endpoint.

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
| SSH connects to the wrong host | `sshHost`/`sshPort` on the hub must be the public portal endpoint, not the internal `http` Service. |
| Hub does not see a new region | Restart the hub after editing `regionsExistingSecret`; values-based `regions` roll automatically. |
| Region workspaces missing from PostgreSQL | The PostgreSQL mirror is configured on the hub only. Region managers own their workspace objects; use the hub API or region namespace for live state. |
