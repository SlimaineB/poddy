# poddy Helm chart

Production-ready Helm chart for the poddy Kubernetes development portal. The
chart runs the portal and SSH bastion as separate Deployments from the same
image.

## Install

The charts are published as OCI artifacts in GHCR. Install the portal chart
directly from the registry:

```bash
helm upgrade --install poddy oci://ghcr.io/slimaineb/charts/poddy \
  -n poddy-system --create-namespace --version 0.1.0 -f values.yaml
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
  -n poddy-system --version 0.1.0 -f templates-values.yaml
```

Replace `0.1.0` with the published chart version when a new release is cut.

Install the templates release in `workspaceNamespace` (the release namespace above when `workspaceNamespace` is empty).

## Publishing

The `Publish Helm charts` workflow packages and pushes both charts to
`oci://ghcr.io/<owner>/charts` when chart files change on `main` or when a `v*`
tag is pushed. On branches the version comes from `Chart.yaml`; on tags the tag
name (without the leading `v`) is used.

## Required configuration

| Parameter | Description |
|-----------|-------------|
| `auth.token` or `auth.existingSecret` or `oidc.enabled` | Authentication is required (token and/or OIDC). |
| `ssh.enabled` | Enable the bastion and UI SSH metadata (default `true`). |
| `ssh.publicHost` | **Required** when SSH enabled. Hostname/IP users connect to. |
| `ssh.authorizedKeys` or `ssh.existingKeysSecret` | **Required** when SSH enabled. Public keys allowed through the bastion. The key comment binds it to an owner (`alice@example.com`, `user:alice@example.com`, or `user:token` for non-email identities) or marks it admin (`admin`, `*`). |
| `ssh.existingHostKeySecret` | **Required** when `bastion.replicaCount > 1`. Secret with private host key. |

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
| `imagePullSecrets` | `[]` | Secrets for portal/bastion images (not workspaces). |
| `ssh.allowUnmappedKeys` | `false` | Legacy: allow keys without an owner comment to reach every workspace. Insecure. |
| `workspaceNamespace` | `""` (release ns) | Namespace for workspace pods/PVCs. Use a dedicated namespace in production. |
| `createWorkspaceNamespace` | `false` | Create the workspace namespace. **Warning**: uninstall deletes it. |
| `workspaceState.configMapName` | `""` (`<fullname>-state`) | ConfigMap for stopped workspace specs. |
| `auth.existingSecret` | `""` | Use existing Secret for token. |
| `auth.secretKey` | `token` | Key in the token Secret. |
| `oidc.enabled` | `false` | Enable OIDC / SSO login. |
| `oidc.issuer` | `""` | OIDC issuer URL. Required when enabled. |
| `oidc.clientId` | `""` | OIDC client ID. Required when enabled. |
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
| `database.url` | `""` | Optional PostgreSQL connection string. Enables metadata mirror and audit. |
| `database.existingSecret` | `""` | Existing Secret with the connection string. Set either `url` or this. |
| `database.urlKey` | `database-url` | Key in the database Secret. |
| `database.autoMigrate` | `true` | Apply embedded schema migrations on startup. |
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
| `ingress.enabled` | `false` | Create Ingress for HTTP. |
| `ingress.className` | `""` | Ingress class (nginx, traefik...). |
| `ingress.hosts` | `[poddy.example.com]` | Ingress hosts & paths. |
| `ingress.tls` | `[]` | TLS config (secretName + hosts). |
| `defaultTemplate` | `base` | Default workspace template. |
| `rbac.create` | `true` | Create Role/RoleBinding. |
| `rbac.configMapName` | `""` (`<fullname>-rbac`) | RBAC ConfigMap name. |
| `rbac.createConfigMap` | `true` | Seed the RBAC ConfigMap on install. |
| `rbac.admins` | `[]` | Global admins (emails, `user:`, `group:`). |
| `rbac.teams` | `{}` | Teams and members. |
| `rbac.templates` | `{}` | Template owners/users/teams. |
| `rbac.extraRules` | `[]` | Extra PolicyRules to append. |
| `serviceAccount.create` | `true` | Create ServiceAccount. |
| `podSecurityContext` | non-root 65532 | Pod security context. |
| `securityContext` | hardened | Container security context. |
| `resources` | 50m/64Mi req, 500m/256Mi lim | Portal resource limits. |
| `podDisruptionBudget.enabled` | `false` | Enable PDB. |

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
          image: {{ .Image | quote }}
          command: [{{ .SSHCommand | quote }}]
```

The portal watches these ConfigMaps in `workspaceNamespace` and reloads without restarting. Keep several versions with `poddy.io/template-version` and mark exactly one `poddy.io/template-active=true`; rollback is a label change. See [templates.md](../../../docs/templates.md#versioning-and-rollback) for the full contract.

## OIDC / SSO

```yaml
oidc:
  enabled: true
  issuer: https://keycloak.example.com/realms/dev
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

Register `https://poddy.example.com/oidc/callback` as the provider redirect URI. The login page shows **Continue with SSO**; the shared token (if configured) remains available for API automation and is a global admin.

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

PostgreSQL is optional. Kubernetes remains the runtime source of truth; the
database receives a metadata mirror and append-only audit events. The chart
injects `PODDY_DATABASE_URL` into both the portal and bastion.

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

Set `autoMigrate: false` when migrations are managed externally. The portal
mirrors RBAC, OIDC users, templates, and workspaces; the bastion records SSH
connections. See [docs/postgres-schema.md](../../../docs/postgres-schema.md)
for the schema, audit actions, backup, and limitations.

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

## RBAC

The portal gets a Role in `workspaceNamespace` with:
- `pods` (get, list, watch, create, delete)
- `pods/exec` (get, create)
- `pods/portforward` (get, create)
- `persistentvolumeclaims` (get, list, create, delete)
- `configmaps` (get, list, watch, create, update, patch)
- `secrets` (get, list, watch, create, update, patch) for external auth tokens

Add extra rules via `rbac.extraRules`.

The bastion has its own ServiceAccount and Role with only `pods` read access and `pods/portforward`. It has no ConfigMap or Secret permissions through the API.

### Workspace lifecycle

The portal can stop, start, and restart workspaces from the UI or API:

- **Stop** deletes the pod, keeps the PVC, and stores the rendered spec in the `workspaceState` ConfigMap.
- **Start** recreates the pod from that spec; **Restart** deletes and recreates it.
- Ephemeral workspaces have no PVC and lose their data on Stop.
- The ConfigMap is created on first use; the chart's `configmaps` Role rule covers it. Set `workspaceState.configMapName` to share or isolate it.


### Admins, teams, and template access

```yaml
rbac:
  configMapName: ""        # default "<fullname>-rbac"
  createConfigMap: true
  admins:
    - admin@example.com
  teams:
    backend:
      members:
        - alice@example.com
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
  -n poddy-system --version 0.1.0 -f values.yaml
```

## Uninstalling

```bash
helm uninstall poddy -n poddy-system
```
If `createWorkspaceNamespace: true`, the workspace namespace and all workspaces are deleted.