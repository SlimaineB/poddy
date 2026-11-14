# poddy Helm chart

Production-ready Helm chart for the poddy Kubernetes development portal. The
chart runs the portal and SSH bastion as separate Deployments from the same
image.

## Install

The charts are published as OCI artifacts in GHCR. Install the portal chart
directly from the registry:

```bash
helm upgrade --install poddy oci://ghcr.io/slimaineb/charts/poddy \
  -n poddy-system --create-namespace --version 0.1.8 -f values.yaml
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
  -n poddy-system --version 0.1.8 -f templates-values.yaml
```

Replace `0.1.8` with the published chart version when a new release is cut.

Install the templates release in `workspaceNamespace` (the release namespace above when `workspaceNamespace` is empty).

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
| `ssh.enabled` | Enable SSH metadata and certificate issuance (default `true`). |
| `bastion.enabled` | Deploy the SSH bastion (default `true`). Set `false` for a hub that only issues certificates. |
| `ssh.publicHost` | **Required** when SSH enabled. Hostname/IP users connect to. |
| `ssh.authorizedKeys` or `ssh.existingKeysSecret` | Optional administrator-managed SSH keys. Teams normally add their own key under **SSH keys** in the portal. The comment binds an administrator-managed key to an owner (`alice@example.com`, `user:alice@example.com`, or `user:token`) or marks it admin (`admin`, `*`). |
| `ssh.existingHostKeySecret` | **Required** when `bastion.replicaCount > 1`. Secret with private host key. |
| `database.url` or `database.existingSecret` | **Required** unless `regionManager.enabled=true`. PostgreSQL 13+ connection string. |

## Key values

| Parameter | Default | Description |
|-----------|---------|-------------|
| `replicaCount` | `1` | Portal replicas. Scales independently from the bastion. |
| `bastion.enabled` | `true` | Deploy the SSH bastion as a separate Deployment. |
| `bastion.replicaCount` | `1` | Bastion replicas. >1 requires `ssh.existingHostKeySecret`. |
| `bastion.resources` | `{}` | Bastion resource requests/limits. |
| `bastion.podDisruptionBudget.enabled` | `false` | Create a PDB for the bastion. |
| `image.repository` | `ghcr.io/your-org/poddy` | Portal and bastion image repository. |
| `image.tag` | `latest` | Portal and bastion image tag. |
| `imagePullSecrets` | `[]` | Secrets for portal/bastion images (use `workspace.imagePullSecrets` for workspaces). |
| `hostAliases` | `[]` | Extra `/etc/hosts` entries for the portal and bastion pods. |
| `ssh.allowUnmappedKeys` | `false` | Legacy: allow keys without an owner comment to reach every workspace. Insecure. |
| `ssh.requireAgent` | `true` | Require the outbound workspace agent tunnel and disable Kubernetes port-forward fallback. Custom workspace images must include `poddy-agent`; restart existing workspaces after upgrading. Set `false` to keep the port-forward fallback. |
| `ssh.agent.enabled` | `true` | Run the bastion agent listener and inject agent URL/token into workspaces. |
| `ssh.agent.port` | `8091` | Private ClusterIP port for the agent listener. |
| `ssh.agent.url` | `""` | Agent URL injected into workspaces. Empty derives the in-cluster Service URL. |
| `ssh.agent.tokenTtl` | `168h` | Workspace agent token lifetime. Restart long-running workspaces before it expires. |
| `ssh.agent.existingSecret` | `""` | Existing Secret with the shared agent HMAC key. |
| `ssh.certTtl` | `15m` | Lifetime of issued SSH certificates. |
| `ssh.ca.existingSecret` | `""` | Existing Secret with the SSH CA private key. Share it across hub and regions. |
| `ssh.ca.keyKey` | `ssh-ca-key` | Key in the CA Secret. |
| `ssh.ca.key` | `""` | Inline CA private key (PEM). Empty generates and preserves a random key. |
| `workspaceNamespace` | `""` (release ns) | Namespace for workspace pods/PVCs. Use a dedicated namespace in production. |
| `createWorkspaceNamespace` | `false` | Create the workspace namespace. **Warning**: uninstall deletes it. |
| `workspace.storageClass` | `""` | Default StorageClass for workspace home PVCs when the template leaves it empty. |
| `workspace.imageRegistry` | `""` | Global fallback registry prefix for workspace and sidecar images. Path prefixes such as `mirror.example.com/cache` are supported and applied once. |
| `workspace.imageRegistryMap` | `{}` | Per-host registry mirrors. Keys are source hosts (`ghcr.io`, `docker.io`, `localhost:5000`) or `*`; values are target prefixes. Exact host wins, then `*`, then `imageRegistry`. An empty value leaves that host untouched. |
| `workspace.imagePullSecrets` | `[]` | Image pull Secrets applied to workspace pods (private registry mirror). |
| `vscodeExtension.id` | `slimaineb.poddy-remote` | Extension identifier shown in the portal. Empty uses the metadata embedded in the image. |
| `vscodeExtension.version` | `0.1.0` | Extension version shown in the portal. Empty uses the metadata embedded in the image. |
| `vscodeExtension.releaseUrl` | GitHub release | Release page linked from the portal. Empty uses the metadata embedded in the image. |
| `vscodeExtension.downloadUrl` | GitHub VSIX | VSIX download linked from the portal. Empty uses the metadata embedded in the image. |
| `workspaceState.configMapName` | `""` (`<fullname>-state`) | Legacy ConfigMap read at startup to migrate stopped workspaces to StatefulSets. |
| `regions` | `[]` | Central hub only. Region list with `name`, `key`, optional `url` fallback, `sshHost`, `sshPort`, `default`. Mounted as `regions.yaml`. |
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
| `externalAuth.tokenSecretName` | `""` (`<fullname>-external-auth`) | Secret for encrypted per-user tokens. |
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
| `ssh.service.type` | `LoadBalancer` | SSH Service type. |
| `ssh.service.port` | `2222` | SSH Service port. |
| `ssh.service.nodePort` | `0` | Optional fixed SSH NodePort. |
| `http.service.type` | `ClusterIP` | HTTP Service type. |
| `http.service.port` | `80` | HTTP Service port. |
| `http.service.nodePort` | `0` | Optional fixed HTTP NodePort. |
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
| `podDisruptionBudget.enabled` | `false` | Enable PDB. |
| `topologySpreadConstraints` | `[]` | Portal spread constraints; defaults to host spread when `replicaCount > 1`. |
| `bastion.topologySpreadConstraints` | `[]` | Bastion spread constraints; defaults to host spread when `bastion.replicaCount > 1`. |

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

The portal watches these ConfigMaps in `workspaceNamespace` and reloads without restarting. `pod.yaml` is a partial overlay: poddy merges it over the internal pod defaults, so templates only declare what they customize. Keep several versions with `poddy.io/template-version` and mark exactly one `poddy.io/template-active=true`; rollback is a label change. See [templates.md](../../../docs/templates.md#versioning-and-rollback) for the full contract.

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

See [docs/oidc.md](../../../docs/oidc.md) for claims, groups, HA sessions, and troubleshooting. A local Dex demo is in [deploy/dex](../../../deploy/dex).

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

The portal stores per-user tokens in a separate Secret, `externalAuth.tokenSecretName` (default `<fullname>-external-auth`). The chart grants `get/list/watch/create/update/patch` on Secrets. `externalAuth.key`/`existingSecret` is required when `replicaCount > 1`.

Register `https://poddy.example.com/external-auth/<id>/callback` with each provider. See [docs/external-auth.md](../../../docs/external-auth.md) for provider defaults, template `externalAuth:` declarations, workspace env vars, and troubleshooting.

## PostgreSQL metadata and audit

PostgreSQL is required for the portal and bastion. It is the control plane for
RBAC, users, quotas, access, shares, tokens, environment variables/secrets, and
append-only audit events. Region managers use the hub instead. The chart injects
`PODDY_DATABASE_URL` into both the portal and bastion.

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
team/user quotas; the bastion records SSH connections. Admins and `rbac.auditors` can read the trail from the **Audit**
page. See [docs/postgres-schema.md](../../../docs/postgres-schema.md) for the
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
bastion:
  replicaCount: 3
```

## Scaling portal and bastion independently

The portal and bastion use the same image but run as separate Deployments:

```yaml
replicaCount: 3

bastion:
  enabled: true
  replicaCount: 2
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
    limits:
      memory: 256Mi
  podDisruptionBudget:
    enabled: true
    minAvailable: 1
```

The portal scales on HTTP load. The bastion scales on SSH connections. The
bastion uses `ssh.existingHostKeySecret`, so SSH clients see a stable host key
across replicas.

## Multi-region

Use one central hub and one `regionManager.enabled=true` release per region. The
region manager opens an outbound WebSocket tunnel to `regionManager.hubUrl`, so
the hub does not need inbound access or VPN peering to the region cluster. The
hub owns auth/RBAC/templates/external auth and routes workspace HTTP/WebSocket
traffic through that tunnel. See the main
[multi-region documentation](../../docs/regions.md) for the Secret format and
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
- `leases` (get, create, update) for distributed workspace lifecycle locks

Add extra rules via `rbac.extraRules`.

The bastion has its own ServiceAccount and Role with only `pods` read access and `pods/portforward`. It has no ConfigMap or Secret permissions through the API.

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

The portal writes the RBAC document to the ConfigMap at runtime. `helm upgrade` re-applies these seed values; set `rbac.createConfigMap: false` after the initial install if admins should manage access only from the UI. See [docs/rbac.md](../../../docs/rbac.md).

## Security

- Non-root UID/GID 65532, read-only rootfs, dropped capabilities, seccomp `RuntimeDefault`.
- Token stored in Secret; cookie `HttpOnly`, `SameSite=Lax`, `Secure` on TLS.
- Workspace pods: `automountServiceAccountToken: false`, `fsGroup: 1000`.

## Upgrading

```bash
helm upgrade poddy oci://ghcr.io/slimaineb/charts/poddy \
  -n poddy-system --version 0.1.8 -f values.yaml
```

## Uninstalling

```bash
helm uninstall poddy -n poddy-system
```
If `createWorkspaceNamespace: true`, the workspace namespace and all workspaces are deleted.