# Production deployment

!!! tip "New to poddy?"
    Start with [Getting started](getting-started.md) for the shortest local
    test, then come back here for production.

> For a local k3s test deployment with separate namespaces, see
> [k3s-test.md](k3s-test.md). This page focuses on production.

## Helm chart

The Helm chart in `deploy/helm/poddy` installs the portal and SSH bastion as separate Deployments. The `deploy/helm/poddy-templates` chart installs workspace templates as a separate release; templates hot-reload without restarting the portal. Both charts are published as OCI artifacts at `oci://ghcr.io/slimaineb/charts/poddy` and `oci://ghcr.io/slimaineb/charts/poddy-templates`.

!!! tip "Amazon EKS"
    For an EKS example with ALB Ingress, an NLB for SSH, Okta OIDC, and shared
    EFS storage, see [Amazon EKS with ALB and EFS](eks.md).

### Prerequisites
- Kubernetes 1.28+
- Helm 3.13+
- Default StorageClass for workspace PVCs
- Container registry access for custom images (published GHCR images are available)
- Ingress controller (nginx, traefik, etc.) for HTTPS
- Optional: LoadBalancer for SSH (or use Ingress TCP)
- Optional: PostgreSQL 13+ for the [metadata mirror and audit log](postgres-schema.md)

### 1. Use the published image

The repository publishes the portal/bastion image to GHCR on `main` and `v*` tags:

```yaml
image:
  repository: ghcr.io/slimaineb/poddy
  tag: "latest"   # or a v* release tag
```

If the GHCR package is private, create an image pull secret and reference it in
the chart values. For source builds, see [Development](development.md).

### 2. Prepare secrets

**Access token** (shared secret for the web UI):
```bash
kubectl create secret generic poddy-auth \
  --from-literal=token="$(openssl rand -hex 32)" \
  -n poddy-system
```

**SSH host key** (required for `bastion.replicaCount > 1`, recommended always):
```bash
ssh-keygen -t ed25519 -N '' -f poddy_host_ed25519
kubectl create secret generic poddy-host-key \
  --from-file=ssh_host_ed25519_key=poddy_host_ed25519 \
  -n poddy-system
```
The key name in the secret **must** match `ssh.hostKeySecretKey` (default `ssh_host_ed25519_key`).

**Authorized keys** (keys allowed to open workspaces via the bastion):
```yaml
# In values.yaml, or create a Secret:
ssh:
  authorizedKeys:
    - "ssh-ed25519 AAAA... alice@example.com"
    - "ssh-ed25519 AAAA... bob@example.com"
    - "ssh-ed25519 AAAA... admin"
```
The comment at the end of each key binds it to a workspace owner:
- `alice@example.com` or `user:alice@example.com` — may only open workspaces owned by `alice@example.com`.
- `user:<identity>` — use this form for non-email identities, for example `user:token` with token auth or `user:alice` with an OIDC username.
- `admin` or `*` — break-glass key that may open every workspace.
- Any other comment (or no comment) is **denied** by default.

With token auth the owner identity is `token`, so the key comment must be `user:token` (a bare `token` comment is unmapped and denied). With OIDC, use the user's email or `user:<username>`.

The bastion checks both the key and the target workspace owner on every `direct-tcpip` request. Workspace pods receive only their owner's key plus admin keys in `SSH_AUTHORIZED_KEY`.

For legacy local setups only, `ssh.allowUnmappedKeys: true` restores the old "any key can reach any workspace" behavior. Do not enable it in production. It is enabled automatically in trusted local mode with no token and no OIDC. In local editor mode, the portal also marks the SSH keys it manages as admin keys so local SSH/VS Code keeps working with token or OIDC auth.

Or use an existing Secret:
```yaml
ssh:
  existingKeysSecret: poddy-ssh-keys
  keysSecretKey: authorized_keys
```

**Optional: PostgreSQL metadata and audit.** Kubernetes remains the source of
truth, but you can mirror RBAC, users, templates, workspaces, and audit events
into PostgreSQL. Create a Secret with the connection string:

```bash
kubectl create secret generic poddy-database \
  --from-literal=database-url='postgres://poddy:secret@postgres.example.com:5432/poddy?sslmode=require' \
  -n poddy-system
```

Then reference it from values:

```yaml
database:
  existingSecret: poddy-database
  urlKey: database-url
  autoMigrate: true
```

The chart injects `PODDY_DATABASE_URL` into the portal and bastion. The portal
applies embedded migrations on startup by default. Set `autoMigrate: false` if
migrations are managed externally. If PostgreSQL is configured but unreachable
at startup, the portal exits; later audit/sync failures are logged without
blocking workspace operations.

### 3. Minimal values.yaml
```yaml
image:
  repository: ghcr.io/slimaineb/poddy
  tag: "latest"

auth:
  existingSecret: poddy-auth

ssh:
  enabled: true
  publicHost: ssh.dev.example.com
  publicPort: 2222
  authorizedKeys:
    - "ssh-ed25519 AAAA... alice@example.com"
  allowUnmappedKeys: false
  existingHostKeySecret: poddy-host-key

bastion:
  enabled: true
  replicaCount: 1

workspaceNamespace: poddy-workspaces
createWorkspaceNamespace: true

# Optional: mirror metadata and audit events to PostgreSQL.
# database:
#   existingSecret: poddy-database
#   urlKey: database-url
#   autoMigrate: true

ingress:
  enabled: true
  className: nginx
  hosts:
    - host: poddy.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: poddy-tls
      hosts:
        - poddy.example.com
```

### 4. Install
```bash
helm upgrade --install poddy oci://ghcr.io/slimaineb/charts/poddy \
  -n poddy-system --create-namespace --version 0.1.0 -f values.yaml

helm upgrade --install poddy-templates oci://ghcr.io/slimaineb/charts/poddy-templates \
  -n poddy-workspaces --version 0.1.0 -f templates-values.yaml
```
If the GHCR chart package is private, run `helm registry login ghcr.io` first. From a source checkout, replace the OCI references with `deploy/helm/poddy` and `deploy/helm/poddy-templates`. Replace `0.1.0` with the published chart version when a new release is cut.

The templates release creates labeled ConfigMaps in its namespace, so install it in `workspaceNamespace` (here `poddy-workspaces`). Templates can also be pushed later with `kubectl apply`; see [templates.md](templates.md#pushing-templates-to-kubernetes).

### 5. Access
- **Web UI**: `https://poddy.example.com` (or port-forward `kubectl -n poddy-system port-forward svc/poddy 8080:80`).
- **Token**: Get it from the secret:
  ```bash
  kubectl -n poddy-system get secret poddy-auth -o jsonpath='{.data.token}' | base64 -d
  ```
- **SSH**: Users connect to `ssh.dev.example.com:2222`. The UI's **SSH** button gives a ready-to-use config with `ProxyJump`.
- **VS Code Web**: When browser VS Code is enabled (`browserVSCode`, default `true` for the built-in templates), the **VS Code Web** button opens the editor in a new tab through the portal. No SSH key or local port-forward is needed.

With SSH enabled, the release creates two Deployments:

```bash
kubectl -n poddy-system get deploy
# NAME            READY   ...
# poddy           1/1     portal
# poddy-bastion   1/1     SSH bastion
```

## Scaling & multi-team

### Capacity guidance

poddy is designed for small and medium engineering teams, not as a multi-tenant SaaS control plane. A single portal replica comfortably handles hundreds of workspaces and low thousands of known users. The practical limits are:

- **Kubernetes API**: every workspace list/create/stop/start is an API call. Large numbers of workspaces or frequent polling from many users increase API load.
- **ConfigMap size**: stopped workspace specs and RBAC/users data live in ConfigMaps (1 MiB limit per ConfigMap). Thousands of stopped workspaces with large rendered specs will hit this limit.
- **Bastion connections**: each SSH session opens a port-forward through a bastion pod. Size `bastion.resources` for concurrent SSH sessions.
- **HA**: portal replicas are stateless except for the OIDC session key (and external-auth key when providers are enabled). Bastion replicas need a shared host key. Use stable Secrets when scaling either Deployment above one replica.

For more than roughly 1,000 concurrent users or 1,000+ workspaces, shard by team using one release/namespace per team, or add API caching, pagination, and the optional [PostgreSQL metadata mirror](postgres-schema.md) for audit and usage reporting. PostgreSQL is a metadata mirror and the audit read path, not the source of truth for workspace or RBAC decisions; it does not remove the ConfigMap size limit by itself.

### Replicas and namespaces

- **Portal replicas**: Set `replicaCount > 1` for HTTP/API load. The portal is stateless; use a stable OIDC session key when OIDC is enabled.
- **Bastion replicas**: Set `bastion.replicaCount` independently. **Must** provide `ssh.existingHostKeySecret` when it is greater than 1, so all bastion replicas share the same host key.
- **Workspace namespace**: `workspaceNamespace` defaults to the release namespace. Set it to a dedicated namespace (e.g., `team-a`) to isolate teams.
  - `createWorkspaceNamespace: true` creates the namespace. **Warning**: `helm uninstall` will delete that namespace and all workspaces inside it. For production, create the namespace yourself (`kubectl create ns team-a`) and set `createWorkspaceNamespace: false`.
  - The portal and bastion Roles/RoleBindings live in `workspaceNamespace`; both ServiceAccounts live in the release namespace. The RoleBinding subjects reference the SAs across namespaces.
- **Multiple teams**: Deploy one release per team with different `workspaceNamespace` and `releaseName`. Install that team's `poddy-templates` release in the same workspace namespace. Or use a single portal with multiple namespaces (requires code changes — currently one namespace per portal).

## RBAC details

The chart creates two least-privilege identities:

**Portal**
- **ServiceAccount** `poddy` (or custom name) in the release namespace.
- **Role** in `workspaceNamespace` with rules:
  - `pods` (get, list, watch, create, delete) — workspace lifecycle.
  - `pods/log` (get) — startup logs in the workspace details dialog.
  - `pods/exec` (get, create) — browser terminal.
  - `pods/portforward` (get, create) — local editor and VS Code helpers.
  - `persistentvolumeclaims` (get, list, create, delete) — persistent volumes.
  - `configmaps` (get, list, watch, create, update, patch) — template ConfigMaps, the RBAC document, user directory, and stopped specs.
  - `secrets` (get, list, watch, create, update, patch) — external-auth token store.
- **RoleBinding** in `workspaceNamespace` binding the Role to the ServiceAccount (cross-namespace).
- Extra rules can be added via `rbac.extraRules` in values.

**Bastion**
- **ServiceAccount** `poddy-bastion` (or custom name) in the release namespace.
- **Role** in `workspaceNamespace` with only:
  - `pods` (get, list, watch) — resolve the target workspace.
  - `pods/portforward` (get, create) — open the SSH tunnel.
- **RoleBinding** in `workspaceNamespace` binding the bastion Role to the bastion ServiceAccount.
- The bastion has **no** ConfigMap or Secret access through the API and cannot create or delete pods.

Neither the portal nor the bastion can access other namespaces or cluster-scoped resources.

### Workspace lifecycle

- **Stop** deletes the workspace pod, keeps its PVC, and stores the rendered pod spec in the `poddy-state` ConfigMap (key `workspaces.yaml`).
- **Start** recreates the pod from the stored spec; **Restart** deletes and recreates it. The PVC is reused.
- **Ephemeral workspaces** (`storage` empty) have no PVC: stopping them loses their data. The UI warns before stopping.
- Stopped workspaces appear with phase `Stopped`; only their owner or a global admin can start/stop/restart/delete them.
- Click a workspace card or name to open its details. The dialog shows metadata plus **startup logs** from init containers and workspace containers. Logs are fetched with the `pods/log` permission and are limited to the last 500 lines per container.
- The state ConfigMap name is `workspaceState.configMapName` (default `<fullname>-state`) in the chart, or `-state-configmap` / `PODDY_STATE_CONFIGMAP`. It is created on first use and covered by the `configmaps` permission.
- The rendered spec is preserved, so a stopped workspace resumes with the same image/resources even if the template changes. Delete and recreate to pick up template changes.

### Template links

Each template declares its own buttons in `template.yaml` under `links:`. They are rendered on workspace cards and versioned with the template.

- Placeholders: `{name}`, `{alias}` (`poddy-<name>`), `{sshHost}`, `{sshPort}`, `{user}` (`dev`), `{template}`, `{image}`.
- `icon` is one of `external`, `vscode`, `jetbrains`, `terminal`, `code`, `link`.
- `newTab: true` opens the link in a new browser tab.
- URLs are validated; `javascript:`, `data:`, and `vbscript:` are rejected.

See [links.md](links.md) for examples.

### Template access and teams

The portal stores global admins, teams, and per-template access in the `poddy-rbac` ConfigMap (key `rbac.yaml`) in `workspaceNamespace`. It creates the ConfigMap on first write if it does not exist; the Helm chart can seed it from `rbac.admins`, `rbac.teams`, and `rbac.templates`.

- Admins see and manage every workspace and template.
- Template owners manage `users`/`teams` for their templates; only admins manage owners and teams.
- A template without an entry is public.
- Workspace ownership is stored in the `poddy.io/owner` pod annotation; non-admins see only their own workspaces.

See [rbac.md](rbac.md) for the document format, matching rules, and API. The chart's `rbac.createConfigMap` defaults to `true`; `helm upgrade` will re-apply the seed values. Set it to `false` after the first install if admins should manage access only from the UI.

## SSH bastion configuration

| Value | Description |
|-------|-------------|
| `ssh.enabled` | Enable the bastion and the UI SSH metadata (default true). |
| `bastion.enabled` | Deploy the bastion as a separate Deployment (default true). Required when `ssh.enabled=true`. |
| `bastion.replicaCount` | Bastion replicas (default 1). >1 requires `ssh.existingHostKeySecret`. |
| `bastion.resources` | Resource requests/limits for the bastion pod. |
| `ssh.addr` | Listen address inside the bastion pod (default `:2222`). |
| `ssh.publicHost` | **Required** when enabled. Hostname/IP users connect to. |
| `ssh.publicPort` | Port users connect to (default `2222`). |
| `ssh.authorizedKeys` | List of public keys. The key comment binds it to an owner (`user:alice@example.com`) or marks it admin (`admin`, `*`). Used to create a ConfigMap. |
| `ssh.allowUnmappedKeys` | Allow keys without an owner comment to access every workspace (legacy, insecure; default `false`). |
| `ssh.existingKeysSecret` | Use an existing Secret instead of ConfigMap. |
| `ssh.existingHostKeySecret` | **Required for `bastion.replicaCount > 1`**. Secret with private host key at `ssh.hostKeySecretKey`. |
| `ssh.service.type` | Service type for SSH (default `LoadBalancer`). |
| `ssh.service.port` | Service port (default `2222`). |

**How it works**: The bastion runs as a separate `poddy-bastion` Deployment on
`:2222`; in local mode it runs inside the portal process. No separate EC2
bastion host or kubeconfig is required. It accepts SSH connections, verifies the
public key, reads the target host from the `direct-tcpip` request
(`poddy-ssh-<name>` or `poddy-<name>`), looks up the workspace pod and its
`poddy.io/owner` annotation, authorizes the key for that owner, then opens a
port-forward to pod port 22 and bridges the connection.

A key mapped to `alice@example.com` cannot reach a workspace owned by `bob@example.com`; the channel is rejected with `not authorized for this workspace`. Admin keys (`admin` or `*`) can reach every workspace for break-glass access.

**Exposing SSH**:
- Use `LoadBalancer` service type (default). The LB IP/DNS becomes `ssh.publicHost`. This is an L4 TCP load balancer, not an HTTP Ingress.
- Or use an Ingress TCP service (nginx stream, Traefik TCP) pointing to the `poddy-ssh` Service, which selects the bastion pods. Set `ssh.service.type: ClusterIP` and configure your LB/Ingress accordingly.
- For local clusters, a NodePort works (`ssh.service.type: NodePort`); users connect to `<node-ip>:<nodePort>`.
- Use a stable `ssh.existingHostKeySecret` so users do not get host-key warnings after restarts or when scaling bastion replicas.

**VS Code from Windows**: Windows VS Code uses Windows OpenSSH, not WSL. Save the config from the **SSH** dialog in `C:\Users\<you>\.ssh\config`, copy your private key to `C:\Users\<you>\.ssh\`, and make sure the `IdentityFile` points to that key. The generated config uses `Host poddy-ssh-*`, so it covers all workspaces and only needs to be added once. The **VS Code** button then opens Remote-SSH through the `poddy-ssh-<name>` alias. A config saved only in WSL is not visible to Windows VS Code and produces `Could not resolve hostname poddy-ssh-<name>`. The generated config accepts the bastion host key on first use (`accept-new`) and disables host-key checking for workspace pods because their host keys are ephemeral.

**Changing keys**: the bastion loads `authorized_keys` at startup. If you use `ssh.existingKeysSecret`, update the Secret and run `kubectl rollout restart deployment/poddy-bastion -n poddy-system`. Existing workspace pods keep their old `SSH_AUTHORIZED_KEY` until they are restarted (stop/start or delete/recreate). The ConfigMap path already triggers a bastion rollout through the chart checksum.

## Ingress & TLS

```yaml
ingress:
  enabled: true
  className: nginx
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt
  hosts:
    - host: poddy.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: poddy-tls
      hosts:
        - poddy.example.com
```
The chart creates an Ingress for the HTTP service (port 80). The portal detects TLS via `r.TLS != nil` and sets the cookie `Secure` flag.

## OIDC / SSO

poddy supports the OpenID Connect authorization code flow with PKCE. When enabled, the login page shows **Continue with SSO**; sessions are stateless, HMAC-signed cookies (no database).

### Provider configuration

- **Redirect URI**: `https://<poddy-host>/oidc/callback`
- **Scopes**: `openid profile email groups` (adjust to your provider; `groups` is optional)
- **Client type**: confidential (with `clientSecret`) or public (PKCE, no secret)

### Helm values

```yaml
oidc:
  enabled: true
  issuer: https://keycloak.example.com/realms/dev
  clientId: poddy
  clientSecret: ""            # prefer existingSecret in production
  redirectUrl: https://poddy.example.com/oidc/callback
  scopes: "openid,profile,email,groups"
  allowedGroups:
    - devs
  allowedUsers: []
  usernameClaim: preferred_username
  emailClaim: email
  groupsClaim: groups
  sessionKey: ""              # required for replicaCount > 1 unless existingSecret
  sessionTtl: 12h
```

Or use an existing Secret:

```bash
kubectl create secret generic poddy-oidc \
  --from-literal=oidc-client-id=poddy \
  --from-literal=oidc-client-secret=... \
  --from-literal=oidc-session-key="$(openssl rand -hex 32)" \
  -n poddy-system
```

```yaml
oidc:
  enabled: true
  issuer: https://keycloak.example.com/realms/dev
  existingSecret: poddy-oidc
```

The chart reads `oidc.clientIdKey` (default `oidc-client-id`), `oidc.clientSecretKey` (default `oidc-client-secret`) and `oidc.sessionKeyKey` (default `oidc-session-key`).

### Flags / environment

| Flag | Environment | Description |
|------|-------------|-------------|
| `-oidc-issuer` | `PODDY_OIDC_ISSUER` | Issuer URL (enables OIDC). |
| `-oidc-client-id` | `PODDY_OIDC_CLIENT_ID` | Client ID. |
| `-oidc-client-secret` | `PODDY_OIDC_CLIENT_SECRET` | Client secret (empty = public client with PKCE). |
| `-oidc-redirect-url` | `PODDY_OIDC_REDIRECT_URL` | Callback URL (derived from request when empty). |
| `-oidc-scopes` | `PODDY_OIDC_SCOPES` | Comma-separated scopes. |
| `-oidc-allowed-groups` | `PODDY_OIDC_ALLOWED_GROUPS` | Comma-separated group allowlist. |
| `-oidc-allowed-users` | `PODDY_OIDC_ALLOWED_USERS` | Comma-separated username/email allowlist. |
| `-oidc-username-claim` | `PODDY_OIDC_USERNAME_CLAIM` | Username claim (default `preferred_username`). |
| `-oidc-email-claim` | `PODDY_OIDC_EMAIL_CLAIM` | Email claim (default `email`). |
| `-oidc-groups-claim` | `PODDY_OIDC_GROUPS_CLAIM` | Groups claim (default `groups`). |
| `-oidc-session-key` | `PODDY_OIDC_SESSION_KEY` | Secret used to sign sessions. |
| `-oidc-session-ttl` | `PODDY_OIDC_SESSION_TTL` | Session lifetime (default `12h`). |

### Claims and groups

The portal reads the username, email and groups from the ID token. If a claim is absent, it also merges claims from the provider's userinfo endpoint.

- **Keycloak**: add a `groups` mapper to the client scope (ID token + userinfo).
- **Microsoft Entra ID**: enable the `groups` claim in the token configuration, or map app roles.
- **Auth0**: add a custom claim with an Action.
- **Google**: no groups claim; use `allowedUsers` or a custom claim.

`allowedGroups` / `allowedUsers` are case-insensitive. Empty lists allow any authenticated user.

### Sessions and HA

- Sessions are signed with `PODDY_OIDC_SESSION_KEY`. If unset, a random key is generated at startup (sessions are lost on restart and not shared across replicas).
- For `replicaCount > 1`, set a stable 32-byte key (or `oidc.existingSecret`). The chart fails if neither is set.
- Default session lifetime is 12h; sign-out clears the cookie.
- Token auth remains available when `auth.token` is set. To require SSO only, leave `auth.token` empty and do not set `PODDY_TOKEN`.

### Callback behind a proxy

Set `oidc.redirectUrl` (or `PODDY_OIDC_REDIRECT_URL`) to the public URL. If empty, the portal derives it from the request and honors `X-Forwarded-Proto` / `X-Forwarded-Host` set by ingress controllers.

### Local test

The repository includes a Dex demo:

```bash
kubectl --context local create namespace poddy   # once
./scripts/dev-oidc.sh
```

It starts Dex, applies a demo RBAC ConfigMap, and runs the portal with OIDC. See [oidc.md](oidc.md#local-demo-with-dex).

To use another provider, start the portal with:

```bash
PODDY_OIDC_ISSUER=http://localhost:8081/realms/dev \
PODDY_OIDC_CLIENT_ID=poddy \
PODDY_OIDC_CLIENT_SECRET=... \
PODDY_OIDC_REDIRECT_URL=http://localhost:8090/oidc/callback \
./poddy -context local -namespace poddy
```

Register `http://localhost:8090/oidc/callback` as a valid redirect URI in the provider.

## External auth for workspaces

External auth is separate from OIDC login. Configure OAuth2 providers and templates can receive refreshed Git tokens inside workspaces. The portal stores per-user tokens in a Secret, so the Role needs `secrets` access (included in the Helm chart and quick-start manifest).

```yaml
externalAuth:
  url: https://poddy.example.com
  existingSecret: poddy-external-auth-config
  providers:
    - id: github
      type: github
      clientId: Iv1.abc123
      existingSecret: poddy-external-auth-config
      clientSecretKey: provider-github-client-secret
```

See [external-auth.md](external-auth.md) for provider defaults, callbacks, template declarations, and troubleshooting.

## Prometheus metrics

The portal can expose Prometheus metrics on a dedicated listener. The listener is
disabled by default when running the binary directly; the Helm chart enables it
on port `9090`.

- **Flag / environment**: `-metrics-addr=:9090` or `PODDY_METRICS_ADDR=:9090`.
  Leave both empty to disable the listener.
- **Helm**: `metrics.enabled` (default `true`), `metrics.service.port` (default
  `9090`). The chart creates a ClusterIP Service named
  `<fullname>-metrics`. Metrics are never routed through the portal Ingress.
- **ServiceMonitor**: set `metrics.serviceMonitor.enabled=true` if the
  Prometheus Operator CRDs are installed. Optional settings include `interval`,
  `scrapeTimeout`, `labels`, `relabelings`, and `metricRelabelings`.

```yaml
metrics:
  enabled: true
  service:
    port: 9090
  serviceMonitor:
    enabled: true
    interval: 30s
    scrapeTimeout: 10s
    labels:
      release: kube-prometheus-stack
```

Metrics include:

| Metric | Labels | Description |
|--------|--------|-------------|
| `poddy_http_requests_total` | `method`, `route`, `status` | HTTP requests handled by the portal. |
| `poddy_http_request_duration_seconds` | `method`, `route` | HTTP request latency histogram. |
| `poddy_http_in_flight_requests` | | Requests currently being handled. |
| `poddy_workspace_operations_total` | `operation`, `result` | Workspace create/delete/stop/start/restart outcomes. |
| `poddy_workspace_create_duration_seconds` | `template`, `result` | Workspace creation latency histogram. |
| `poddy_auth_attempts_total` | `method`, `result` | Successful and denied authentication attempts. |
| `poddy_template_reloads_total` | `result` | Template catalog reloads. |
| `poddy_workspaces` | `phase`, `template` | Current workspaces, queried from Kubernetes on scrape. |
| `poddy_build_info` | `version`, `go_version` | Build metadata. |

The standard Go runtime and process metrics are also exposed. The `route` label
uses the Go `http.ServeMux` pattern (for example
`GET /api/workspaces/{name}`), which keeps cardinality bounded.

The metrics listener has no authentication and must not be exposed publicly.
Keep the metrics Service `ClusterIP` and restrict access with a NetworkPolicy or
your monitoring stack's scrape configuration.

## Security hardening

The chart applies:
- **PodSecurityContext**: `runAsNonRoot: true`, `runAsUser: 65532`, `runAsGroup: 65532`, `fsGroup: 65532`, `seccompProfile: RuntimeDefault`.
- **ContainerSecurityContext**: `allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true`, `capabilities.drop: [ALL]`.
- **SSH key isolation**: keys are mapped to owners by their `authorized_keys` comment. Unmapped keys are denied. The bastion re-checks the workspace owner for every forwarded connection, and workspace pods only receive their owner's key plus admin keys.
- **Admin keys are powerful**: an `admin`/`*` SSH key can reach every workspace. Protect admin private keys and rotate them like any other cluster credential.
- **Ephemeral host key**: If `ssh.existingHostKeySecret` is not set, an `emptyDir` at `/tmp/poddy` holds a generated host key. This is fine for a single replica but **not** for HA.
- **Network policies**: Not included; add your own to restrict pod-to-pod traffic. In particular, restrict access to the SSH Service to trusted networks.
- **Token rotation**: Update the Secret and rollout the deployment (`kubectl rollout restart deployment/poddy -n poddy-system`).
- **OIDC**: Prefer OIDC over the shared token for human access. Set `allowedGroups`/`allowedUsers` to restrict sign-in, and use a stable `oidc.sessionKey` for HA.
- **Namespace isolation**: For hard tenant isolation, deploy one release per team/namespace. SSH isolation prevents cross-owner access inside one portal, but a shared namespace still shares network and storage classes.
- **Local editor keys**: In local editor mode the portal marks keys it manages as admin keys, which is intended for a single operator workstation. Cluster deployments use `-editor=` and do not add local keys.
- **Audit**: With PostgreSQL enabled, the portal and bastion write control-plane audit events to `audit_events` (workspace lifecycle, terminal/VS Code, SSH, team/template access, external auth). Audit writes are best effort and do not block operations; keep Kubernetes audit logs for stronger guarantees.

## Workspace images

The portal does not build workspace images. The publish workflow pushes the
built-in workspace images to GHCR:
- Base: `ghcr.io/slimaineb/poddy-workspace-base:latest` (Debian 12)
- Go: `ghcr.io/slimaineb/poddy-workspace:latest` (Go 1.25.5)
- Node.js: `ghcr.io/slimaineb/poddy-workspace-node:latest` (Node.js 22)
- Python: `ghcr.io/slimaineb/poddy-workspace-python:latest` (Python 3.11 + uv)
- Custom: build on `ghcr.io/slimaineb/poddy-workspace-base` (see [templates.md](templates.md#building-custom-workspace-images)).

The built-in `base`, `go`, `node`, and `python` templates use the GHCR images
above. If you use a private registry, add `imagePullSecrets` to the pod template.

## Quick-start manifest (no Helm)

`deploy/poddy.yaml` is a single-file manifest for local testing. It runs the
portal and bastion in one Deployment; the Helm chart splits them. It creates:
- Namespace `poddy`
- ServiceAccount, Role, RoleBinding
- ConfigMap `poddy-ssh-keys` (add your keys; with the default token auth comment them `user:token`, or use `admin`)
- ConfigMap `poddy-rbac` (demo seed; the portal updates it)
- ConfigMap `poddy-state` (created on first stop; stopped workspace specs)
- Secret `poddy-token` (change the token!)
- Deployment `poddy` (non-root, read-only rootfs, ephemeral host key)
- Service `poddy` (ports 80 and 2222)

Apply:
```bash
kubectl apply -f deploy/poddy.yaml
```
**Not for production** — no TLS, no stable host key, no ingress, single replica.

## Do I need PostgreSQL?

**No. Kubernetes is the source of truth.**
- Workspaces = Pods + PVCs (listable, watchable, deletable via API).
- Templates = versioned files / ConfigMaps / Helm values (GitOps-friendly).
- No separate DB is required to run poddy.

**Workspace ownership, RBAC, and stopped specs work without a database:**
- The owner is a pod annotation (`poddy.io/owner`).
- Admins, teams, and template access are a small ConfigMap (`poddy-rbac`), updated through the API.
- Stopped workspace specs are a small ConfigMap (`poddy-state`).
- Users seen through OIDC are a small ConfigMap (`poddy-users`), written on sign-in. It is only a suggestion directory for access/team editors; authentication and sessions remain with the OIDC provider.

**When you might add PostgreSQL:**
- You need metadata to survive loss of the cluster.
- You need a queryable audit log of workspace, terminal, VS Code, SSH, team, template, and external-auth actions.
- You need usage accounting or reporting.
- You want a durable copy of the metadata mirror for restore tooling.

Set `database.url` or `database.existingSecret` in the Helm chart, or
`PODDY_DATABASE_URL` outside Helm. The portal mirrors RBAC, users, templates,
and workspaces, and records audit events; the bastion records SSH audit events.
Kubernetes remains authoritative and PostgreSQL is never read for access
decisions. If PostgreSQL is configured but unavailable at startup, the process
exits; if it fails later, sync/audit errors are logged and workspace operations
continue. Back up PVCs separately with volume snapshots. See
[PostgreSQL schema](postgres-schema.md) for migrations, tables, and restore
guidance.

## Troubleshooting

| Symptom | Check |
|---------|-------|
| Portal pod `CrashLoopBackOff` | `kubectl logs -n poddy-system deploy/poddy` — usually missing token, SSH keys, host key, or an unreachable configured PostgreSQL URL. |
| SSH "Host key verification failed" | `ssh.existingHostKeySecret` not set and replica restarted; or user's `known_hosts` has old key. |
| SSH "not authorized for this workspace" | The key's comment is mapped to another owner. Use `user:<owner-identity>` (for example `user:alice@example.com` or `user:token`) or an `admin` key. |
| SSH "unknown public key" | The key is not in `ssh.authorizedKeys` / the keys Secret. |
| Workspace stuck `Pending` | `kubectl describe pod poddy-<name> -n <ws-ns>` — usually PVC not bound (no StorageClass) or image pull error. |
| Browser terminal disconnects | Portal pod restarted? Check portal logs. WebSocket `/ws` requires the same token/cookie. |
| No audit rows in PostgreSQL | Check `PODDY_DATABASE_URL`, portal/bastion logs for `audit:` or `metadata:` errors, and that migrations ran (`SELECT * FROM poddy.schema_migrations`). |
| VS Code does nothing or cannot connect | Install the SSH config from the **SSH** dialog, then retry. Cluster mode opens `vscode://` in a new window; the server-side launch endpoint is only available in local editor mode. |
| **VS Code Web** button is missing | The workspace pod has no `code-server` container. Check that the template declares `browserVSCode` with default `"true"` (or explicitly `"true"`), and that the workspace was not created with `browserVSCode: "false"`. |
| **VS Code Web** says the workspace is not running | Start the workspace and retry. The proxy only forwards to running pods. |
| "unknown template" | Template not loaded. Check `poddy.io/template=true` on the ConfigMap, the `poddy.io/template-name` label / `template.yaml` name, and the portal logs. |
| PVC not created | `storage` parameter empty or not declared in template. |
| Metrics endpoint refuses connections | Check `metrics.enabled`, the `-metrics-addr` flag, and portal logs for `metrics listening on`. The metrics listener is separate from the portal HTTP port. |

## Validation and smoke test

The chart ships a `values.schema.json` (validated by Helm) and a `helm test` hook:

```bash
helm lint deploy/helm/poddy -f values.yaml
helm test poddy -n poddy-system
```

The test pod calls `/healthz` through the HTTP Service. To check metrics:

```bash
kubectl -n poddy-system port-forward svc/poddy-metrics 9090:9090
curl -s http://127.0.0.1:9090/metrics | grep '^poddy_'
```

## Chart publishing

The `Publish Helm charts` workflow pushes both charts to
`oci://ghcr.io/<owner>/charts` when chart files change on `main` or a `v*` tag
is pushed. On branches the chart version comes from `Chart.yaml`; on tags the
tag name without the leading `v` is used. Make the GHCR chart packages public
if unauthenticated users should install them.

## Upgrading

```bash
helm upgrade poddy oci://ghcr.io/slimaineb/charts/poddy \
  -n poddy-system --version 0.1.0 -f values.yaml
```
- Changing `workspaceNamespace` or `createWorkspaceNamespace` may require manual cleanup.
- Changing `ssh.existingHostKeySecret` requires a rollout.
- Template ConfigMaps are hot-reloaded. Updating a template does not restart the portal; invalid updates keep the previous catalog active.
- Versioned template ConfigMaps (`poddy.io/template-version` + `poddy.io/template-active`) allow rollback by moving the active label or changing the templates chart `active` value. See [templates.md](templates.md#versioning-and-rollback).

## Uninstalling

```bash
helm uninstall poddy -n poddy-system
```
If `createWorkspaceNamespace: true`, the workspace namespace and **all workspaces/PVCs** are deleted. To preserve workspaces, create the namespace outside Helm.