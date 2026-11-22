# poddy Helm chart

Production-ready Helm chart for the poddy Kubernetes development portal. The
portal serves the web UI, the API, the workspace agent broker, and SSH from a
single Deployment. Regions connect outbound to the hub; no SSH bastion or
per-region public SSH endpoint is required.

## Install

The charts are published as OCI artifacts in GHCR. Install the portal chart
directly from the registry:

```bash
helm upgrade --install poddy oci://ghcr.io/slimaineb/charts/poddy \
  -n poddy-system --create-namespace --version 0.1.10 -f values.yaml
```

If the GHCR package is private, authenticate first:

```bash
helm registry login ghcr.io
```

Or install from a source checkout:

```bash
helm upgrade --install poddy deploy/helm/poddy -n poddy-system --create-namespace -f values.yaml
```

Workspace templates are a separate release:

```bash
helm upgrade --install poddy-templates oci://ghcr.io/slimaineb/charts/poddy-templates \
  -n poddy-system --version 0.1.10 -f templates-values.yaml
```

Replace `0.1.10` with the published chart version when a new release is cut.

Install the templates release in `workspaceNamespace` (the release namespace above when `workspaceNamespace` is empty).

## Desktop SSH transports

The Poddy extension supports two desktop transports and the portal chooses one:

- With `mesh.enabled=true`, the portal returns `transport: mesh`. The bundled
  `poddy-connect` helper joins the Headscale/Tailscale tailnet and dials
  `ws-<workspace>` over WireGuard. The chart does not create an SSH Service or
  SSH listener. `mesh.authKey` (or `mesh.existingSecret`) is required.
- Otherwise the portal returns `transport: portal`. The extension opens an
  authenticated WebSocket to `/api/desktop/ssh`, and the portal reaches the
  workspace through the Kubernetes port-forward API or the region tunnel. This
  mode does not need mesh or a public SSH Service.

The manual SSH listener and `ssh.publicHost` remain relevant for `ssh` and
JetBrains Gateway when mesh is disabled. See the website documentation for
[SSH access](https://slimaineb.github.io/poddy/ssh/) and
[Headscale mesh](https://slimaineb.github.io/poddy/mesh/).

## Metrics

Metrics are enabled by default on port `9090` and served by a separate
ClusterIP Service named `<fullname>-metrics`; they are not exposed through the
portal Ingress. Set `metrics.enabled=false` to disable the listener and Service.

If the cluster has the Prometheus Operator CRDs, enable a ServiceMonitor:

```yaml
metrics:
  serviceMonitor:
    enabled: true
    interval: 30s
    scrapeTimeout: 10s
    labels:
      release: kube-prometheus-stack
```

## Publishing

The `Publish Helm charts` workflow packages and pushes both charts to
`oci://ghcr.io/<owner>/charts` when chart files change on `main` or when a `v*`
tag is pushed. On branches the version comes from `Chart.yaml`; on tags the tag
name (without the leading `v`) is used.

## Required configuration

| Parameter | Description |
|-----------|-------------|
| `auth.token` or `auth.existingSecret` or `oidc.enabled` | Authentication is required (token and/or OIDC). |
| `ssh.enabled` | Enable the portal SSH listener (default `true`). Ignored for the listener when `mesh.enabled=true`. |
| `ssh.publicHost` | **Required** when `ssh.enabled=true` and `mesh.enabled=false`. Hostname/IP users connect to. |
| `ssh.authorizedKeys` or `ssh.existingKeysSecret` | Optional administrator-managed SSH keys. Teams normally add their own key under **SSH keys** in the portal. The comment binds an administrator-managed key to an owner (`alice@example.com`, `user:alice@example.com`, or `user:token`) or marks it admin (`admin`, `*`). |
| `ssh.existingHostKeySecret` | Optional Secret with the SSH host key. Empty generates and preserves one; set it when `replicaCount > 1` to manage rotation yourself. |
| `database.url` or `database.existingSecret` | **Required** unless `regionManager.enabled=true`. PostgreSQL 13+ connection string. |

## Key values

| Parameter | Default | Description |
|-----------|---------|-------------|
| `replicaCount` | `1` | Portal replicas. SSH and the agent broker scale with the portal; replicas use Kubernetes Leases to forward sessions to the replica that owns a workspace agent or region tunnel. |
| `image.repository` | `ghcr.io/your-org/poddy` | Portal image repository. |
| `image.tag` | `latest` | Portal image tag. |
| `imagePullSecrets` | `[]` | Secrets for portal images (use `workspace.imagePullSecrets` for workspaces). |
| `hostAliases` | `[]` | Extra `/etc/hosts` entries for portal pods. |
| `ssh.allowUnmappedKeys` | `false` | Legacy: allow keys without an owner comment to reach every workspace. Insecure. |
| `ssh.requireAgent` | `true` | Require the outbound workspace agent tunnel and disable Kubernetes port-forward fallback. Custom workspace images must include `poddy-agent`; restart existing workspaces after upgrading. Set `false` to keep the port-forward fallback. |
| `ssh.drainDelay` | `3s` | Reject new SSH connections immediately, then keep the listener open for this long so endpoints drain. |
| `ssh.drainTimeout` | `20s` | Keep existing SSH sessions during graceful shutdown. |
| `ssh.agent.enabled` | `true` | Run the portal agent listener and inject agent URL/token into hub workspaces. Region managers skip the agent and use the region tunnel. |
| `ssh.agent.port` | `8091` | Private ClusterIP port for the agent listener and peer forwarding. |
| `ssh.agent.url` | `""` | Agent URL injected into workspaces. Empty derives the in-cluster Service URL. |
| `ssh.agent.tokenTtl` | `1h` | Workspace agent token lifetime. Tokens renew over the tunnel, are refreshed in the workspace Secret for reconnects, and are scoped to one workspace. |
| `ssh.agent.existingSecret` | `""` | Existing Secret with the shared agent HMAC key. |
| `ssh.certTtl` | `15m` | Lifetime of issued SSH certificates. |
| `ssh.ca.existingSecret` | `""` | Existing Secret with the SSH CA private key. Share it across hub and regions. |
| `ssh.ca.keyKey` | `ssh-ca-key` | Key in the CA Secret. |
| `ssh.ca.key` | `""` | Inline CA private key (PEM). Empty generates and preserves a random key. |
| `mesh.enabled` | `false` | Run workspace agents on a Headscale/Tailscale tailnet and make mesh the preferred desktop transport. The chart skips the portal SSH Service and listener when enabled. |
| `mesh.controlUrl` | `""` | Mesh control server URL. Required when `mesh.enabled=true` unless `mesh.headscale.enabled=true`. |
| `mesh.authKey` / `mesh.existingSecret` | `""` | Reusable mesh preauth key for workspaces and desktop clients. Required when `mesh.enabled=true` unless `mesh.existingSecret` is set. |
| `workspaceNamespace` | `""` (release ns) | Namespace for workspace pods/PVCs. Use a dedicated namespace in production. |
| `createWorkspaceNamespace` | `false` | Create the workspace namespace. **Warning**: uninstall deletes it. |
| `dotfiles.url` | `""` | Portal-wide fallback dotfiles repository for users without a personal setting. |
| `dotfiles.branch` | `""` | Fallback branch. Empty uses `main`. |
| `workspace.storageClass` | `""` | Default StorageClass for workspace home PVCs when the template leaves it empty. |
| `workspace.imageRegistry` | `""` | Global fallback registry prefix for workspace and sidecar images. Path prefixes such as `mirror.example.com/cache` are supported and applied once. |
| `workspace.imageRegistryMap` | `{}` | Per-host registry mirrors. Keys are source hosts (`ghcr.io`, `docker.io`, `localhost:5000`) or `*`; values are target prefixes. Exact host wins, then `*`, then `imageRegistry`. An empty value leaves that host untouched. |
| `workspace.imagePullSecrets` | `[]` | Image pull Secrets applied to workspace pods (private registry mirror). |
| `vscodeExtension.id` | `slimaineb.poddy-remote` | Extension identifier shown in the portal. Empty uses the metadata embedded in the image. |
| `vscodeExtension.version` | `0.1.1` | Extension version shown in the portal. Empty uses the metadata embedded in the image. |
| `vscodeExtension.releaseUrl` | GitHub release | Release page linked from the portal. Empty uses the metadata embedded in the image. |
| `vscodeExtension.downloadUrl` | GitHub VSIX | VSIX download linked from the portal. Empty uses the metadata embedded in the image. |
| `workspaceState.configMapName` | `""` (`<fullname>-state`) | Legacy ConfigMap read at startup to migrate stopped workspaces to StatefulSets. |
| `regions` | `[]` | Central hub only. Region list with `name`, `key`, optional `url` fallback, `default`. Mounted as `regions.yaml`. Region SSH and API traffic use the outbound region tunnel. |
| `regionsExistingSecret` | `""` | Existing Secret containing `regionsKey` instead of inline `regions`. |
| `regionsKey` | `regions.yaml` | Key in the regions Secret. |
| `regionManager.enabled` | `false` | Run this release as a region manager trusted by a central hub. |
| `regionManager.name` | `""` | Region identifier. Required when enabled. |
| `regionManager.hubUrl` | `""` | Central hub public URL. The region manager uses it for the outbound tunnel and external auth. Required when enabled. |
| `regionManager.key` | `""` | Shared key used to verify hub identities. Required unless `regionManager.existingSecret` is set. |
| `regionManager.existingSecret` | `""` | Existing Secret containing `regionManager.keyKey`. |
| `desktop.secret` | `""` | Stable key for VS Code desktop tickets. Recommended for `replicaCount > 1`. |
| `desktop.existingSecret` | `""` | Existing Secret containing `desktop.secretKey`. |
| `auth.existingSecret` | `""` | Use existing Secret for token. |
| `auth.tokenAdmin` | `false` | Treat the shared token as a global admin. Keep false in multi-user deployments. |
| `auth.secretKey` | `token` | Key in the token Secret. |
| `oidc.enabled` | `false` | Enable OIDC / SSO login. |
| `oidc.issuer` | `""` | OIDC issuer URL (the expected `iss` claim). Required when enabled. |
| `oidc.discoveryUrl` | `""` | Optional discovery URL when it differs from the issuer (defaults to issuer). |
| `oidc.authorizationUrl` | `""` | Optional browser-facing authorization endpoint override. |
| `oidc.clientId` | `""` | OIDC client ID. Required when enabled unless `oidc.existingSecret` is set. |
| `oidc.clientSecret` | `""` | OIDC client secret (empty = public client with PKCE). |
| `oidc.existingSecret` | `""` | Secret with client ID, client secret and session key. |
| `oidc.redirectUrl` | `""` | Public callback URL (`https://host/oidc/callback`). |
| `oidc.scopes` | `openid,profile,email,groups` | OIDC scopes. |
| `oidc.allowedGroups` | `[]` | Group allowlist. Empty allows any authenticated user. |
| `oidc.allowedUsers` | `[]` | Username/email allowlist. |
| `oidc.usernameClaim` | `preferred_username` | Username claim. |
| `oidc.emailClaim` | `email` | Email claim. |
| `oidc.groupsClaim` | `groups` | Groups claim. |
| `oidc.sessionKey` | `""` | Session signing key. Required for `replicaCount > 1` unless `existingSecret`. |
| `oidc.sessionTtl` | `12h` | Session lifetime. |
| `externalAuth.url` | `""` | Public portal URL reachable from workspaces. |
| `externalAuth.redirectBase` | `""` | Public base URL for OAuth callbacks. |
| `externalAuth.key` | `""` | Token encryption/workspace signing key. Required for HA. |
| `externalAuth.existingSecret` | `""` | Secret containing `externalAuth.keyKey` and provider secrets. |
| `externalAuth.tokenSecretName` | `""` (`<fullname>-external-auth`) | Legacy Secret imported into PostgreSQL once; not updated after import. |
| `externalAuth.providers` | `[]` | OAuth2 provider list. |
| `database.url` | `""` | PostgreSQL connection string. Required unless `regionManager.enabled=true`. |
| `database.existingSecret` | `""` | Existing Secret with the connection string. Set either `url` or this. |
| `database.urlKey` | `database-url` | Key in the database Secret. |
| `database.autoMigrate` | `true` | Apply embedded schema migrations on startup. |
| `database.connectTimeout` | `30s` | Retry window before startup fails when PostgreSQL or the schema is not ready. |
| `ssh.addr` | `:2222` | Bastion listen address in pod. |
| `ssh.publicPort` | `2222` | Port users connect to. |
| `ssh.existingKeysSecret` | `""` | Use existing Secret for authorized_keys. |
| `ssh.keysSecretKey` | `authorized_keys` | Key in the SSH keys Secret/ConfigMap. |
| `ssh.existingHostKeySecret` | `""` | Secret with host private key. |
| `ssh.hostKeySecretKey` | `ssh_host_ed25519_key` | Key in the host key Secret. |
| `ssh.service.type` | `ClusterIP` | SSH Service type. Opt in to `LoadBalancer`/`NodePort` explicitly. |
| `ssh.service.port` | `2222` | SSH Service port. |
| `ssh.service.nodePort` | `0` | Optional fixed SSH NodePort. |
| `ssh.service.loadBalancerClass` | `""` | Load balancer class, e.g. `eks.amazonaws.com/nlb` (EKS Auto Mode) or `service.k8s.aws/nlb` (AWS Load Balancer Controller >= 2.9). |
| `http.service.type` | `ClusterIP` | HTTP Service type. |
| `http.service.port` | `80` | HTTP Service port. |
| `http.service.nodePort` | `0` | Optional fixed HTTP NodePort. |
| `http.service.loadBalancerClass` | `""` | Load balancer class for the portal Service. |
| `networkPolicy.enabled` | `true` | Default-deny NetworkPolicy for workspace pods with DNS/control-plane/internet allow rules. |
| `networkPolicy.workspace.egress.allowedCIDRs` | `[]` | Extra allowed destinations (internal mirrors, databases). |
| `networkPolicy.workspace.egress.allowedNamespaces` | `[]` | Extra allowed namespace destinations. |
| `monitoring.enabled` | `true` | Master switch for in-product workspace monitoring APIs and UI. |
| `monitoring.live` | `true` | Live CPU/memory from metrics-server in workspace details. |
| `monitoring.history` | `true` | Historical charts from Prometheus. Requires `monitoring.prometheus.url`. |
| `monitoring.global` | `true` | Admin-only portal-wide monitoring view. |
| `monitoring.costs.enabled` | `false` | Enable FinOps cost data. |
| `monitoring.costs.visibleToUsers` | `false` | Show cost data to workspace owners instead of admins only. |
| `monitoring.costs.cpuCoreHour` | `0` | Price per CPU core per hour. `0` disables CPU cost. |
| `monitoring.costs.memoryGBHour` | `0` | Price per memory GiB per hour. `0` disables memory cost. |
| `monitoring.costs.storageGBMonth` | `0` | Price per provisioned storage GiB per month. `0` disables storage cost. |
| `monitoring.costs.currency` | `USD` | Currency label shown next to cost estimates. |
| `monitoring.prometheus.url` | `""` | Prometheus base URL. Empty disables history. |
| `monitoring.prometheus.existingSecret` | `""` | Existing Secret containing a Prometheus bearer token. |
| `monitoring.prometheus.tokenKey` | `token` | Key in the Prometheus token Secret. |
| `metrics.enabled` | `true` | Expose Prometheus metrics on a dedicated listener and ClusterIP Service. |
| `metrics.service.port` | `9090` | Metrics listener and Service port. |
| `metrics.service.annotations` | `{}` | Annotations for the metrics Service. |
| `metrics.serviceMonitor.enabled` | `false` | Render a `monitoring.coreos.com/v1` ServiceMonitor. Requires the Prometheus Operator CRDs. |
| `metrics.serviceMonitor.interval` | `30s` | ServiceMonitor scrape interval. |
| `metrics.serviceMonitor.scrapeTimeout` | `10s` | ServiceMonitor scrape timeout. |
| `metrics.serviceMonitor.labels` | `{}` | Extra labels for the ServiceMonitor. |
| `metrics.serviceMonitor.relabelings` | `[]` | Prometheus relabeling configs. |
| `metrics.serviceMonitor.metricRelabelings` | `[]` | Prometheus metric relabeling configs. |
| `ingress.enabled` | `false` | Create Ingress for HTTP. |
| `ingress.className` | `""` | Ingress class (nginx, traefik...). |
| `ingress.hosts` | `[poddy.example.com]` | Ingress hosts & paths. |
| `ingress.tls` | `[]` | TLS config (secretName + hosts). |
| `defaultTemplate` | `base` | Default workspace template. |
| `rbac.create` | `true` | Create Role/RoleBinding. |
| `rbac.configMapName` | `""` (`<fullname>-rbac`) | RBAC ConfigMap name. |
| `rbac.createConfigMap` | `true` | Seed the RBAC ConfigMap on install. |
| `rbac.admins` | `[]` | Global admins (emails, `user:`, `group:`). |
| `rbac.auditors` | `[]` | Read-only audit viewers (emails, `user:`, `group:`). |
| `rbac.teams` | `{}` | Teams, members, and optional quota defaults. |
| `rbac.userQuotas` | `{}` | Per-user quota overrides (maxWorkspaces, cpu, memory, storage). |
| `rbac.templates` | `{}` | Template owners/users/teams. |
| `rbac.extraRules` | `[]` | Extra PolicyRules to append. |
| `serviceAccount.create` | `true` | Create ServiceAccount. |
| `podSecurityContext` | non-root 65532 | Pod security context. |
| `securityContext` | hardened | Container security context. |
| `resources` | 50m/64Mi req, 500m/256Mi lim | Portal resource limits. |
| `portal.shutdownDrainDelay` | `3s` | Fail readiness and reject new HTTP requests for this long before closing the listener. |
| `portal.terminationGracePeriodSeconds` | `30` | Portal pod grace period; keep above `shutdownDrainDelay` plus the HTTP shutdown timeout. |
| `minReadySeconds` | `5` | Delay before a new portal pod is considered available. |
| `podDisruptionBudget.enabled` | `true` | Create a portal PDB automatically when `replicaCount > 1`; set `false` to opt out. |
| `podDisruptionBudget.minAvailable` | `1` | Pods guaranteed available during voluntary disruptions. |
| `podDisruptionBudget.unhealthyPodEvictionPolicy` | `AlwaysAllow` | Allow eviction of unhealthy pods even when the PDB budget is exhausted. |
| `topologySpreadConstraints` | `[]` | Portal spread constraints; defaults to host spread when `replicaCount > 1`. |

## Workspace templates

Templates are not part of this chart. Install the separate [`poddy-templates`](../poddy-templates) chart, or apply labeled ConfigMaps directly:

```yaml
metadata:
  labels:
    poddy.io/template: "true"
    poddy.io/template-name: node
    poddy.io/template-version: "1.0.0"
    poddy.io/template-active: "true"
data:
  template.yaml: |
    name: node
    displayName: Node.js
    image: ghcr.io/org/poddy-workspace-node:1
  pod.yaml: |
    apiVersion: v1
    kind: Pod
    spec:
      containers:
        - name: workspace
          env:
            - name: NODE_ENV
              value: development
```

The portal watches these ConfigMaps in `workspaceNamespace` and reloads without restarting. `pod.yaml` is a partial overlay: poddy merges it over the internal pod defaults, so templates only declare what they customize. Keep several versions with `poddy.io/template-version` and mark exactly one `poddy.io/template-active=true`; rollback is a label change. See [the template contract](https://slimaineb.github.io/poddy/templates/#versioning-and-rollback) for the full contract.

## OIDC / SSO

```yaml
oidc:
  enabled: true
  issuer: https://keycloak.example.com/realms/dev
  # discoveryUrl: http://keycloak.internal/realms/dev
  # authorizationUrl: https://keycloak.example.com/realms/dev/protocol/openid-connect/auth
  clientId: poddy
  clientSecret: ""
  existingSecret: poddy-oidc
  redirectUrl: https://poddy.example.com/oidc/callback
  allowedGroups:
    - devs
  sessionTtl: 12h
```

Create the secret with the keys `oidc-client-id`, `oidc-client-secret`, and `oidc-session-key`:

```bash
kubectl create secret generic poddy-oidc \
  --from-literal=oidc-client-id=poddy \
  --from-literal=oidc-client-secret=... \
  --from-literal=oidc-session-key="$(openssl rand -hex 32)" \
  -n poddy-system
```

Register `https://poddy.example.com/oidc/callback` as the provider redirect URI. `issuer` is the expected `iss` claim and is separate from the callback; use `discoveryUrl` or `authorizationUrl` when discovery or the browser authorization endpoint uses a different URL. The login page shows **Continue with SSO**; the shared token (if configured) remains available for API automation and is not a global admin unless `auth.tokenAdmin=true`.

See [OIDC](https://slimaineb.github.io/poddy/oidc/) for claims, groups, HA sessions, and troubleshooting. A local Dex demo is in [deploy/dex](../../../deploy/dex).

## External auth for workspaces

External auth connects a user's GitHub/GitLab/etc. account and injects a refreshed token into matching workspaces. It is separate from OIDC login.

```yaml
externalAuth:
  url: https://poddy.example.com
  redirectBase: https://poddy.example.com
  existingSecret: poddy-external-auth-config
  keyKey: external-auth-key
  providers:
    - id: github
      type: github
      displayName: GitHub
      clientId: Iv1.abc123
      existingSecret: poddy-external-auth-config
      clientSecretKey: provider-github-client-secret
    - id: gitlab
      type: gitlab
      clientId: gitlab-client
      existingSecret: poddy-external-auth-config
      clientSecretKey: provider-gitlab-client-secret
```

Create the Secret (or put `key`/`clientSecret` in values for a quick test; the chart creates `poddy-external-auth-config`):

```bash
kubectl create secret generic poddy-external-auth-config   --from-literal=external-auth-key="$(openssl rand -hex 32)"   --from-literal=provider-github-client-secret=...   --from-literal=provider-gitlab-client-secret=...   -n poddy-system
```

Per-user tokens are stored AES-GCM encrypted in PostgreSQL. The legacy `externalAuth.tokenSecretName` Secret (default `<fullname>-external-auth`) is imported once when the table is empty. The chart grants `get/list/watch/create/update/patch` on Secrets for SSH keys, environment secrets, and the legacy import. `externalAuth.key`/`existingSecret` is required when `replicaCount > 1`.

Register `https://poddy.example.com/external-auth/<id>/callback` with each provider. See [external authentication](https://slimaineb.github.io/poddy/external-auth/) for provider defaults, template `externalAuth:` declarations, workspace env vars, and troubleshooting.

## PostgreSQL control plane and audit

PostgreSQL is required for the hub portal. It is the control plane for RBAC,
users, quotas, access, shares, tokens, environment variables/secrets, and
append-only audit events. Region managers use the hub instead. The chart injects
`PODDY_DATABASE_URL` into the portal.

```yaml
database:
  url: postgres://poddy:secret@postgres.example.com:5432/poddy?sslmode=require
  autoMigrate: true
```

Or use an existing Secret in the release namespace:

```bash
kubectl create secret generic poddy-database \
  --from-literal=database-url='postgres://poddy:secret@postgres.example.com:5432/poddy?sslmode=require' \
  -n poddy-system
```

```yaml
database:
  existingSecret: poddy-database
  urlKey: database-url
  autoMigrate: true
```

Set `autoMigrate: false` when migrations are managed externally. If PostgreSQL
or the schema is not ready, the portal retries for `database.connectTimeout`
(default `30s`) and then exits. Poddy does not deploy PostgreSQL.
With the official `postgres` image on an EBS-backed PVC, set
`PGDATA=/var/lib/postgresql/data/pgdata`; otherwise `initdb` fails on
`lost+found`. The portal mirrors RBAC, OIDC users, templates, workspaces, and
team/user quotas; the portal records SSH connections. Admins and `rbac.auditors` can read the trail from the **Audit**
page. See [PostgreSQL schema](https://slimaineb.github.io/poddy/postgres-schema/) for the
schema, audit actions, backup, and limitations.

## Validation and tests

`values.schema.json` validates the main values during `helm lint`, `helm template`, and `helm install`. A test hook checks the portal `/healthz` through the Service:

```bash
helm test poddy -n poddy-system
```

## SSH host key for HA

```bash
ssh-keygen -t ed25519 -N '' -f poddy_host_ed25519
kubectl create secret generic poddy-host-key \
  --from-file=ssh_host_ed25519_key=poddy_host_ed25519 \
  -n poddy-system
```
Then set:
```yaml
ssh:
  existingHostKeySecret: poddy-host-key
  hostKeySecretKey: ssh_host_ed25519_key
replicaCount: 3
```

## Scaling the portal

The portal serves HTTP, WebSocket, the agent broker, and SSH from the same
Deployment, so `replicaCount` scales every path together:

```yaml
replicaCount: 3
podDisruptionBudget:
  enabled: true
  minAvailable: 1

resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    memory: 256Mi
```

Portal replicas use Kubernetes Leases to find the replica that owns a workspace
agent or region tunnel, then forward the raw stream over the internal agent port.
Set `ssh.existingHostKeySecret` so SSH clients see a stable host key across
replicas.

## Headscale HA and PostgreSQL

The managed Headscale control plane defaults to SQLite and one replica. For a
production control plane, point it at an external PostgreSQL database and use a
shared ReadWriteMany volume for the Headscale private keys:

```yaml
mesh:
  headscale:
    replicas: 2
    storageAccessMode: ReadWriteMany
    database:
      type: postgres
      postgres:
        host: headscale-db.internal
        port: 5432
        name: headscale
        user: headscale
        ssl: require
        existingSecret: headscale-db
        passwordKey: password
  bootstrap:
    enabled: false
  existingSecret: poddy-mesh
```

Multi-replica Headscale requires a shared RWX claim (or `existingClaim`) for
`private.key`, `noise_private.key`, and `derp_server_private.key`. It also
requires the per-pod mesh bootstrap sidecar to be disabled; create the mesh
preauth keys once and store them in `mesh.existingSecret`. Back up the
PostgreSQL database and the private-key claim together. For a fully managed
control plane, disable the in-chart Headscale and set `mesh.controlUrl` to an
external Tailscale/Headscale endpoint.

## Single public IP and TLS endpoint

Portal and Headscale can share the same load balancer, IP, and TCP port while
keeping separate hostnames:

```yaml
ingress:
  enabled: true
  hosts:
    - host: poddy.example.com
      paths:
        - path: /
          pathType: Prefix
mesh:
  enabled: true
  headscale:
    host: mesh.example.com
```

Point both DNS names at the same ingress address. Keep two hostnames instead of
serving Headscale under a path such as `/hs`: the Tailscale client hardcodes
`/ts2021` at the root of the control server, and Headscale does not support an
HTTP path prefix. Sharing the load balancer still gives users one public IP and
one TLS port. STUN continues to use its own UDP port.

## Multi-region

Use one central hub and one `regionManager.enabled=true` release per region. The
region manager opens an outbound WebSocket tunnel to `regionManager.hubUrl`, so
the hub does not need inbound access or VPN peering to the region cluster. The
hub owns auth/RBAC/templates/external auth and routes workspace HTTP/WebSocket
traffic through that tunnel. See the main
[multi-region documentation](https://slimaineb.github.io/poddy/regions/) for the Secret format and
network requirements.

## RBAC

The portal gets a Role in `workspaceNamespace` with:
- `pods` (get, list, watch, create, delete)
- `pods/log` (get) for startup logs in the workspace details dialog
- `pods/exec` (get, create)
- `pods/portforward` (get, create)
- `persistentvolumeclaims` (get, list, create, delete)
- `configmaps` (get, list, watch, create, update, patch)
- `secrets` (get, list, watch, create, update, patch) for external auth tokens
- `leases` (get, list, watch, create, update, delete) for workspace lifecycle locks and tunnel presence

Add extra rules via `rbac.extraRules`.


### Workspace lifecycle

The portal can stop, start, and restart workspaces from the UI or API:

- **Stop** scales the workspace StatefulSet to zero, keeps the PVC, and strips dynamic credentials from the pod template.
- **Start** refreshes those credentials and scales back to one; **Restart** refreshes them and recreates the pod.
- Ephemeral workspaces have no PVC and lose their data on Stop.
- The portal needs `statefulsets` and `services` Role rules in addition to pods, PVCs, and Secrets. The legacy `workspaceState` ConfigMap is only read at startup to migrate stopped workspaces.


### Admins, auditors, teams, and template access

```yaml
rbac:
  configMapName: ""        # default "<fullname>-rbac"
  createConfigMap: true
  admins:
    - admin@example.com
  auditors:
    - security@example.com
  teams:
    backend:
      members:
        - alice@example.com
      quota:
        maxWorkspaces: 5
        cpu: "4"
        memory: "8Gi"
        storage: "50Gi"
  userQuotas:
    alice@example.com:
      maxWorkspaces: 2
      cpu: "2"
  templates:
    go:
      owners:
        - dev@example.com
      users: []
      teams:
        - backend
```

The portal writes the RBAC document to the ConfigMap at runtime. `helm upgrade` re-applies these seed values; set `rbac.createConfigMap: false` after the initial install if admins should manage access only from the UI. See [RBAC](https://slimaineb.github.io/poddy/rbac/).

## Security

- Non-root UID/GID 65532, read-only rootfs, dropped capabilities, seccomp `RuntimeDefault`.
- Token stored in Secret; cookie `HttpOnly`, `SameSite=Lax`, `Secure` on TLS.
- Workspace pods: `automountServiceAccountToken: false`, `fsGroup: 1000`.

## Upgrading

```bash
helm upgrade poddy oci://ghcr.io/slimaineb/charts/poddy \
  -n poddy-system --version 0.1.10 -f values.yaml
```

## Uninstalling

```bash
helm uninstall poddy -n poddy-system
```
If `createWorkspaceNamespace: true`, the workspace namespace and all workspaces are deleted.