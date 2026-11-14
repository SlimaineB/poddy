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
    EBS and EFS storage, see [Amazon EKS with ALB, EBS, and EFS](eks.md).

### Prerequisites
- Kubernetes 1.28+
- Helm 3.13+
- Default StorageClass for workspace PVCs
- Container registry access for custom images (published GHCR images are available)
- Ingress controller (nginx, traefik, etc.) for HTTPS
- Optional: LoadBalancer for SSH (or use Ingress TCP)
- PostgreSQL 13+ for the [control plane, audit log, and environment secrets](postgres-schema.md)

### 1. Use the published image

The repository publishes the portal/bastion image to GHCR on `main` and `v*` tags:

```yaml
image:
  repository: ghcr.io/slimaineb/poddy
  tag: "latest"   # or a v* release tag
```

If the GHCR package is private, create an image pull secret and reference it in
the chart values. For source builds, see [Development](development.md).

Registry overrides are separate:

- **Portal and bastion**: set `image.repository`, `image.tag`, and
  `imagePullSecrets`.
- **Workspace and sidecar images**: set `workspace.imageRegistry` as a global
  mirror fallback, or `workspace.imageRegistryMap` for per-host mappings such
  as `ghcr.io` and `docker.io`. Set `workspace.imagePullSecrets` for a private
  registry. Individual templates can still set `image` or override a sidecar.
- **Helm chart**: install from your OCI mirror or from the source path
  `deploy/helm/poddy` instead of `oci://ghcr.io/slimaineb/charts/poddy`.

### 2. Prepare secrets

**Access token** (shared secret for the web UI):
```bash
kubectl create secret generic poddy-auth \
  --from-literal=token="$(openssl rand -hex 32)" \
  -n poddy-system
```

**OIDC / SSO** (recommended for humans). The portal supports OpenID Connect with
PKCE. Users sign in with their identity provider instead of a shared token.
Create a Secret with your provider's client credentials:

```bash
kubectl create secret generic poddy-oidc \
  --from-literal=oidc-client-id=poddy \
  --from-literal=oidc-client-secret="..." \
  --from-literal=oidc-session-key="$(openssl rand -hex 32)" \
  -n poddy-system
```

**SSH keys**: nothing to do here. Users add their own public key from the
portal's **SSH keys** page after signing in. Each key is bound to their
identity and only reaches their own workspaces. Administrator-managed keys are
optional break-glass keys; see [RBAC and SSH keys](rbac.md) if you need them.

**PostgreSQL control plane and audit.** PostgreSQL is required for the portal
and bastion. It is authoritative for RBAC, OIDC users, encrypted external auth
tokens, share links, access state, quotas, and environment variables/secrets.
The RBAC and user ConfigMaps become materialized mirrors; templates and
workspaces remain Kubernetes-first and are mirrored for audit and recovery.
Region managers use the hub instead and do not need database credentials. Create
a Secret with the connection string:

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
migrations are managed externally. If PostgreSQL or the schema is not ready, the
portal retries for `database.connectTimeout` (default `30s`) and then exits. If
the database becomes unavailable later, workspace operations continue but
control-plane reads fail until PostgreSQL returns.

On the first start with PostgreSQL, the portal imports the existing RBAC and
user ConfigMaps, the external auth Secret, and (when an encryption key is
configured) the share Secret. The import only runs when the corresponding table
is empty. Set a stable `PODDY_EXTERNAL_AUTH_KEY` (or `PODDY_OIDC_SESSION_KEY` or
`PODDY_DESKTOP_SECRET`) so encrypted tokens, share links, and environment
secrets survive restarts. If no key is set, the database URL is the fallback
key; keep it stable. The old external auth and share Secrets are not updated
after the switch.

#### PostgreSQL on EBS

Poddy does not deploy PostgreSQL. With the official `postgres` image on an
EBS-backed PVC, set `PGDATA` to a subdirectory. Otherwise `initdb` fails because
the volume root contains `lost+found`.

```yaml
spec:
  containers:
    - name: postgres
      image: postgres:16
      env:
        - name: POSTGRES_DB
          value: poddy
        - name: POSTGRES_USER
          value: poddy
        - name: POSTGRES_PASSWORD
          valueFrom:
            secretKeyRef:
              name: postgres
              key: password
        - name: PGDATA
          value: /var/lib/postgresql/data/pgdata
      volumeMounts:
        - name: data
          mountPath: /var/lib/postgresql/data
```

### 3. Minimal values.yaml
```yaml
image:
  repository: ghcr.io/slimaineb/poddy
  tag: "latest"

auth:
  existingSecret: poddy-auth
  # Keep false in multi-user deployments. A shared admin token can access
  # every workspace.
  tokenAdmin: false

oidc:
  enabled: true
  existingSecret: poddy-oidc
  issuer: https://sso.example.com/realms/dev
  clientId: poddy
  redirectUrl: https://poddy.example.com/oidc/callback
  scopes: "openid,profile,email,groups"
  # allowedGroups: ["devs"]  # restrict sign-in to a group

ssh:
  enabled: true
  publicHost: ssh.dev.example.com
  publicPort: 2222

bastion:
  enabled: true
  replicaCount: 1

workspaceNamespace: poddy-workspaces
createWorkspaceNamespace: true

workspace:
  # Use when the cluster has no default StorageClass, for example on EKS.
  storageClass: ""
  # Optional registry mirror for workspace and sidecar images.
  imageRegistry: ""
  # Optional per-host mirrors; exact host wins, then "*", then imageRegistry.
  imageRegistryMap: {}
  # Existing Secrets in the workspace namespace for a private registry.
  imagePullSecrets: []

# Required unless regionManager.enabled=true.
database:
  existingSecret: poddy-database
  urlKey: database-url
  autoMigrate: true

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
  -n poddy-system --create-namespace --version 0.1.8 -f values.yaml

helm upgrade --install poddy-templates oci://ghcr.io/slimaineb/charts/poddy-templates \
  -n poddy-workspaces --version 0.1.8 -f templates-values.yaml
```
From a source checkout, replace the OCI references with `deploy/helm/poddy` and `deploy/helm/poddy-templates`. Replace `0.1.8` with the published chart version when a new release is cut.

The templates release creates labeled ConfigMaps in its namespace, so install it in `workspaceNamespace` (here `poddy-workspaces`). Templates can also be pushed later with `kubectl apply`; see [templates.md](templates.md#adding-a-template).

### 5. Access
- **Web UI**: `https://poddy.example.com` (or port-forward `kubectl -n poddy-system port-forward svc/poddy 8080:80`).
- **Token**: Get it from the secret:
  ```bash
  kubectl -n poddy-system get secret poddy-auth -o jsonpath='{.data.token}' | base64 -d
  ```
- **SSH**: Users connect to `ssh.dev.example.com:2222`. The UI's **SSH** button gives a ready-to-use config with `ProxyCommand`.
- **Desktop IDEs**: The **VS Code** and **Cursor** buttons use the Poddy extension and Remote-SSH. The **PyCharm** button opens JetBrains Gateway with the same SSH config. The **JupyterLab** button opens port `8888` through the portal proxy.
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
- **ConfigMap size**: `poddy-rbac` and `poddy-users` are mirrors of PostgreSQL and stay well under the 1 MiB per-ConfigMap limit. PostgreSQL holds the authoritative records. Workspace specs live in StatefulSets, not ConfigMaps.
- **Bastion connections**: each SSH session opens a multiplexed stream to the workspace agent. When `ssh.requireAgent=false`, the bastion falls back to a Kubernetes port-forward. Size `bastion.resources` for concurrent SSH sessions.
- **HA**: portal replicas are stateless except for the OIDC session key, the external-auth/share encryption key, and region tunnel sessions. Bastion replicas need a shared host key. Use stable Secrets when scaling either Deployment above one replica. When `replicaCount` or `bastion.replicaCount` is above one and `topologySpreadConstraints` is empty, the chart adds a `kubernetes.io/hostname` spread with `ScheduleAnyway`. Enable the portal and bastion PDBs for node drains. When `regions` is configured, keep the hub at one replica or use sticky routing because tunnel sessions live in the hub process.

For more than roughly 1,000 concurrent users or 1,000+ workspaces, shard by team using one release/namespace per team, or add API caching and pagination. PostgreSQL is the source of truth for RBAC, OIDC users, external auth tokens, share links, workspace access, quotas, and environment variables; it is also a metadata mirror and audit read path for workspaces and templates. Kubernetes remains the runtime source of truth for workspaces and templates.

### HA state matrix

| State | Stored in | Shared across portal replicas |
|-------|-----------|-------------------------------|
| Workspace StatefulSets, Pods and PVCs | Kubernetes | yes |
| Workspace pod template and schedule state | StatefulSet `poddy-<workspace>` | yes, through the API and retry-on-conflict |
| Legacy stopped workspace specs | ConfigMap `poddy-state` | migrated at startup |
| RBAC, teams, template ownership | PostgreSQL `rbac_documents`; ConfigMap `poddy-rbac` mirror | yes; PostgreSQL is authoritative when configured |
| OIDC users | PostgreSQL `users`; ConfigMap `poddy-users` mirror | yes; PostgreSQL is authoritative when configured |
| Templates | Labeled ConfigMaps | yes |
| Access requests, grants, presence | PostgreSQL (`access_*`) | yes |
| User and team quotas | PostgreSQL (`user_quotas`, `team_quotas`) | yes |
| External auth tokens | PostgreSQL `external_auth_tokens` | yes; requires a stable encryption key |
| Workspace share links | PostgreSQL `share_links` | yes; requires a stable encryption key |
| Environment variables and secrets | PostgreSQL `env_vars`; per-workspace Secret | yes; requires a stable encryption key for secrets |
| OIDC sessions | Signed cookie | yes when every replica uses the same `PODDY_OIDC_SESSION_KEY` |
| Desktop tickets | Signed ticket | yes when every replica uses the same stable key |
| Region tunnels | Hub process memory | no; keep the hub at one replica or use sticky routing |

Set `replicaCount` above one only with the session key and a stable external-auth/share encryption key in Secrets. Enable the portal and bastion PodDisruptionBudgets, and give bastion replicas a shared SSH host key. See [PostgreSQL metadata and audit](postgres-schema.md) for the tables and their retention.

### Kubernetes client rate limits

The Kubernetes client defaults to 5 QPS with a burst of 10, which throttles a
busy portal and makes terminals and browser VS Code feel slow. poddy raises the
default to 50 QPS / 100 burst. Tune it with `kubernetes.qps` and
`kubernetes.burst` in the Helm values, or `PODDY_K8S_QPS` / `PODDY_K8S_BURST`
(or `-k8s-qps` / `-k8s-burst`). Set `kubernetes.qps: 0` to keep the client-go
default. Raise the burst as well when many users open terminals or IDE proxies
at the same time; a high QPS with a low burst still throttles short spikes.

### Replicas and namespaces

- **Portal replicas**: Set `replicaCount > 1` for HTTP/API load. Workspace stop/start/restart/delete operations use Kubernetes Lease locks, so replicas do not overwrite each other's state. Use a stable OIDC session key when OIDC is enabled, and set `desktop.secret` (or `externalAuth.key`) for stable desktop IDE tickets. When `regions` is configured, keep the hub at one replica or use sticky routing so region tunnel sessions stay reachable.
- **Bastion replicas**: Set `bastion.replicaCount` independently. **Must** provide `ssh.existingHostKeySecret` when it is greater than 1, so all bastion replicas share the same host key.
- **PDBs**: set `podDisruptionBudget.enabled=true` and `bastion.podDisruptionBudget.enabled=true` when running more than one replica. PDBs are not created automatically, and `minAvailable` must leave room for at least one healthy replica during drains.
- **Audit access**: grant `rbac.auditors` to security operators who need the read-only **Audit** page. Audit reads use PostgreSQL; region managers proxy audit reads through the hub.
- **DNS overrides**: set `hostAliases` to add `/etc/hosts` entries to the portal and bastion pods, for example when OIDC or a database hostname is not resolvable through cluster DNS.

### Multi-region

Multi-region uses one central hub and one manager per region. The hub owns
authentication, RBAC, users, templates, the workspace list, the UI, and external
auth tokens. Region managers own the Kubernetes API connection, workspace
StatefulSets, PVCs, legacy stopped-state ConfigMap migration, terminal/desktop
IDE proxying, and the region SSH bastion.

See [Regions](regions.md) for the full step-by-step checklist to add, verify,
rotate, and remove a region.

```text
Browser ──► central hub ── signed identity ──► region tunnel (eu) ──► region manager ──► pods
                       └─ signed identity ──► region tunnel (us) ──► region manager ──► pods

region manager ── outbound WSS ──► central hub
```

Each region manager opens an outbound WebSocket tunnel to the hub. No VPN
peering or hub-to-region inbound access is required. The hub sends
`X-Poddy-Region-Identity`, an HMAC-signed short-lived identity (2 minutes)
containing the user and admin flag. Region managers reject every untrusted
request, including direct browser requests without the signature. Workspace
operations, logs, terminal WebSocket, browser VS Code, SSH details, desktop IDE
tickets, and external-auth workspace callbacks are routed by the hub through
the tunnel. SSH public keys are replicated from the hub registry to every
region.

Central hub values:

```yaml
regionsExistingSecret: poddy-regions
regionsKey: regions.yaml
```

The Secret must contain a `regions.yaml` file:

```yaml
regions:
  - name: eu
    displayName: Europe
    key: <same random key as regionManager.key in the eu cluster>
    sshHost: ssh.eu.example.com
    sshPort: 2222
    lat: 50.1109
    lon: 8.6821
    default: true
  - name: us
    displayName: United States
    key: <same random key as regionManager.key in the us cluster>
    sshHost: ssh.us.example.com
    sshPort: 2222
    lat: 39.0438
    lon: -77.4874
```

`lat` and `lon` are optional coordinates for the World map view. Regions
without them fall back to coordinates based on the region name, then to
`0, 0`. `url` is optional; omit it for tunnel-only routing. If set, the hub
falls back to direct HTTP when the tunnel is down, which still requires a
network path from the hub to the region.

The top bar region filter applies to workspace lists, topology, and the world map.

Region manager values:

```yaml
regionManager:
  enabled: true
  name: eu
  hubUrl: https://poddy.example.com
  existingSecret: poddy-region-key
  keyKey: region-key
replicaCount: 2
ssh:
  enabled: true
  publicHost: ssh.eu.example.com
  existingHostKeySecret: poddy-host-key
```

The hub ingress must forward WebSocket upgrades for
`/api/internal/region/tunnel`. Region keys are shared secrets: rotate them by
updating the hub Secret and the region Secret, then restarting the region
Deployment. The hub tolerates a region being unavailable for list/topology by
returning partial data and logging the failure; workspace-specific requests
return `502` until the region tunnel reconnects. Tunnel sessions live in the
hub process, so run a single hub replica or configure sticky routing when using
region tunnels. Cross-region placement is explicit at creation time; there is
no automatic failover of an existing workspace between regions.
- **Workspace namespace**: `workspaceNamespace` defaults to the release namespace. Set it to a dedicated namespace (e.g., `team-a`) to isolate teams.
  - `createWorkspaceNamespace: true` creates the namespace. **Warning**: `helm uninstall` will delete that namespace and all workspaces inside it. For production, create the namespace yourself (`kubectl create ns team-a`) and set `createWorkspaceNamespace: false`.
  - The portal and bastion Roles/RoleBindings live in `workspaceNamespace`; both ServiceAccounts live in the release namespace. The RoleBinding subjects reference the SAs across namespaces.
  - Set `workspace.storageClass` when the cluster has no default StorageClass. The value is used only when the template's `storageClass` parameter is empty.
  - Set `workspace.imageRegistry` for a global mirror fallback and `workspace.imageRegistryMap` for per-host mirrors (`ghcr.io`, `docker.io`, `localhost:5000`, or `*`). Set `workspace.imagePullSecrets` to existing Secrets in `workspaceNamespace` for a private mirror.
- **Multiple teams**: Deploy one release per team with different `workspaceNamespace` and `releaseName`. Install that team's `poddy-templates` release in the same workspace namespace. Or use a single portal with multiple namespaces (requires code changes — currently one namespace per portal).

## Volume backups

The **Volumes** page lists assigned and orphaned PVCs and can create, restore,
and delete gzip tar backups in S3-compatible object storage. Backups are
disabled by default.

```yaml
backup:
  enabled: true
  helperImage: busybox:1.36
  s3:
    endpoint: https://minio.example.com:9000
    region: us-east-1
    bucket: poddy-backups
    prefix: poddy-backups
    useSSL: true
    existingSecret: poddy-backup
    accessKeyKey: access-key
    secretKeyKey: secret-key
```

Create the bucket first and grant the credentials list/get/put/delete access to
the configured prefix. The chart can create a Secret from
`backup.s3.accessKey`/`secretKey` when `existingSecret` is empty. In a
multi-region deployment, configure the same bucket on every region manager; the
hub routes backup and delete to the owning region and restore to the target
region. The helper image must
contain `sh`, `tar`, and `gzip`. Live backups are allowed while the workspace
runs, with a crash-consistency warning; restores require a stopped target
workspace. Copying a backup to another region requires the same bucket and
prefix in both regions. See
[Volumes and backups](volumes.md) for the full configuration, API, and
troubleshooting reference.

## RBAC details

The chart creates two least-privilege identities:

**Portal**
- **ServiceAccount** `poddy` (or custom name) in the release namespace.
- **Role** in `workspaceNamespace` with rules:
  - `pods` (get, list, watch, create, delete) — workspace lifecycle.
  - `pods/log` (get) — startup logs in the workspace details dialog.
  - `pods/exec` (get, create) — browser terminal.
  - `pods/portforward` (get, create) — local editor and desktop IDE helpers.
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

- **Stop** scales the workspace StatefulSet to zero, keeps its PVC, and strips dynamic credentials from the pod template.
- **Start** refreshes dynamic credentials (SSH keys, external auth, agent token) and scales to one; **Restart** refreshes them and recreates the pod. The PVC is reused.
- **Upgrade migration**: on startup the portal converts stopped entries from the legacy `poddy-state` ConfigMap into stopped StatefulSets and removes them from the ConfigMap. Running bare pods stay visible and are adopted on their next stop or restart.
- **Auto-stop schedules** are stored with the workspace state. The portal evaluates them every 30 seconds using the configured IANA timezone; outside the daily start/stop window it stops the pod, and at the next window it starts it again only if the schedule stopped it. A manual stop is never auto-started.
- **Ephemeral workspaces** (`storage` empty) have no PVC: stopping them loses their data. The UI warns before stopping.
- Stopped workspaces appear with phase `Stopped`; only their owner or a global admin can start/stop/restart/delete them.
- Click a workspace card or name to open its details. The dialog shows metadata plus **startup logs** from init containers and workspace containers. Logs are fetched with the `pods/log` permission and are limited to the last 500 lines per container.
- The template is preserved, so a stopped workspace resumes with the same image/resources even if the template changes. Delete and recreate to pick up template changes. `workspaceState.configMapName` / `-state-configmap` now only points at the legacy ConfigMap read during migration.
- The portal needs `statefulsets` and `services` permissions in addition to pods, PVCs, and Secrets.

### Template links

Each template declares its own buttons in `template.yaml` under `links:`. They are rendered on workspace cards and versioned with the template.

- Placeholders: `{name}`, `{alias}` (`poddy-ssh-<name>` or `poddy-ssh-<region>-<name>`), `{sshHost}`, `{sshPort}`, `{user}` (`dev`), `{template}`, `{image}`.
- `icon` is one of `external`, `vscode`, `cursor`, `pycharm`, `jetbrains`, `jupyterlab`, `terminal`, `code`, `link`.
- `newTab: true` opens the link in a new browser tab.
- URLs are validated; `javascript:`, `data:`, and `vbscript:` are rejected.

See [links.md](links.md) for examples.

### Template access and teams

The portal stores global admins, teams, and per-template access in PostgreSQL `rbac_documents` and reconciles the `poddy-rbac` ConfigMap (key `rbac.yaml`) in `workspaceNamespace` as a mirror. It creates the ConfigMap on first write if it does not exist; the Helm chart can seed it from `rbac.admins`, `rbac.teams`, and `rbac.templates` for the initial import.

- Admins see and manage every workspace and template.
- Template owners manage `users`/`teams` for their templates; only admins manage owners and teams.
- A template without an entry is public.
- Workspace ownership is stored in the `poddy.io/owner` pod annotation; non-admins see only their own workspaces.

See [rbac.md](rbac.md) for the document format, matching rules, and API. The chart's `rbac.createConfigMap` defaults to `true`; `helm upgrade` will re-apply the seed values. Set it to `false` after the first install if admins should manage access only from the UI.

## Prometheus metrics

The portal exposes Prometheus metrics on a dedicated listener. The listener is
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
      release: kubernetes-prometheus-stack
```

The metrics listener has no authentication and must not be exposed publicly.
Keep the metrics Service `ClusterIP` and restrict access with a NetworkPolicy or
your monitoring stack's scrape configuration.

## SSH bastion configuration

| Value | Description |
|-------|-------------|
| `ssh.enabled` | Enable SSH metadata and certificate issuance (default true). |
| `bastion.enabled` | Deploy the bastion as a separate Deployment (default true). Set false for a hub that only issues certificates. |
| `bastion.replicaCount` | Bastion replicas (default 1). >1 requires `ssh.existingHostKeySecret`. |
| `bastion.resources` | Resource requests/limits for the bastion pod. |
| `ssh.addr` | Listen address inside the bastion pod (default `:2222`). |
| `ssh.publicHost` | **Required** when enabled. Hostname/IP users connect to. |
| `ssh.publicPort` | Port users connect to (default `2222`). |
| `auth.tokenAdmin` | Treat the shared token as a global admin (default `false`). Keep false with OIDC or multiple users. |
| `ssh.authorizedKeys` | Optional administrator-managed public keys. The comment binds a key to an owner (`user:alice@example.com`) or marks it admin (`admin`, `*`). Teams normally use the portal's **SSH keys** page instead. |
| `ssh.allowUnmappedKeys` | Allow keys without an owner comment to access every workspace (legacy, insecure; default `false`). |
| `ssh.requireAgent` | Require the outbound workspace agent tunnel and disable the Kubernetes port-forward fallback (default `true`). Set `false` to keep the fallback. |
| `ssh.agent.enabled` | Inject the agent URL/token and run the bastion agent listener (default `true`). |
| `ssh.agent.sidecar` | Run `poddy-agent` in a dedicated sidecar with its own memory limit (default `true`). Set `false` to use the legacy in-workspace agent; custom images must then include `poddy-agent`. |
| `ssh.agent.image` | Agent sidecar image. Empty uses the portal image, which contains `/usr/local/bin/poddy-agent`. If private, add its pull Secret to `workspace.imagePullSecrets`. |
| `ssh.agent.port` | Private ClusterIP port for the agent listener (default `8091`). |
| `ssh.agent.url` | Agent URL injected into workspaces. Empty derives `http://<fullname>-agent.<namespace>.svc:<port>`. |
| `ssh.agent.tokenTtl` | Workspace agent token lifetime (default `168h`). |
| `ssh.agent.existingSecret` | Existing Secret with the shared agent HMAC key instead of the generated one. |
| `ssh.certTtl` | Lifetime of issued SSH certificates (default `15m`). |
| `ssh.ca.existingSecret` | Existing Secret with the SSH CA private key. Share it across the hub and regions. |
| `ssh.ca.keyKey` | Key in the CA Secret (default `ssh-ca-key`). |
| `ssh.ca.key` | Inline CA private key (PEM). Empty generates and preserves a random key. |
| `ssh.existingKeysSecret` | Use an existing Secret instead of ConfigMap. |
| `ssh.existingHostKeySecret` | **Required for `bastion.replicaCount > 1`**. Secret with private host key at `ssh.hostKeySecretKey`. |
| `ssh.service.type` | Service type for SSH (default `LoadBalancer`). |
| `ssh.service.port` | Service port (default `2222`). |

**How it works**: The bastion runs as a separate `poddy-bastion` Deployment on
`:2222`; in local mode it runs inside the portal process. No separate EC2
bastion host or kubeconfig is required. It accepts SSH connections, verifies a
public key or short-lived certificate, reads the target host from the
`direct-tcpip` request (`poddy-ssh-<name>` or `poddy-<name>`), looks up the
workspace pod and its `poddy.io/owner` annotation, authorizes the identity for
that owner, then opens a multiplexed stream to the workspace agent or, as a
fallback, a Kubernetes port-forward to pod port 22.

A key mapped to `alice@example.com` cannot reach a workspace owned by `bob@example.com`; the channel is rejected with `not authorized for this workspace`. Admin keys (`admin` or `*`) can reach every workspace for break-glass access.

The default agent sidecar has a 16Mi memory request and 128Mi limit, so a
workspace memory spike cannot OOM-kill it. The workspace container is granted
`SYS_RESOURCE` so `poddy-ssh-start` can lower `sshd`'s OOM score; the
`poddy-shell` wrapper resets user sessions to the default score. See
[SSH access](ssh.md#memory-pressure-and-oom-protection).

**Exposing SSH**:
- Use `LoadBalancer` service type (default). The LB IP/DNS becomes `ssh.publicHost`. This is an L4 TCP load balancer, not an HTTP Ingress.
- Or use an Ingress TCP service (nginx stream, Traefik TCP) pointing to the `poddy-ssh` Service, which selects the bastion pods. Set `ssh.service.type: ClusterIP` and configure your LB/Ingress accordingly.
- For local clusters, a NodePort works (`ssh.service.type: NodePort`); users connect to `<node-ip>:<nodePort>`.
- Use a stable `ssh.existingHostKeySecret` so users do not get host-key warnings after restarts or when scaling bastion replicas.

**Desktop IDEs**: the **VS Code** and **Cursor** buttons target desktop editors, not WSL. In cluster mode the portal returns a signed, two-minute ticket; the Poddy extension (`slimaineb.poddy-remote`, install the VSIX from the latest GitHub release once per machine in VS Code or Cursor) generates `~/.ssh/poddy_<workspace>_ed25519`, enrolls the public key under the signed-in identity, writes a `# >>> poddy:<workspace>` block in the user's real `~/.ssh/config`, waits for the workspace Secret to propagate, and opens `vscode-remote://ssh-remote+poddy-desktop-<workspace>/home/dev`. Users do not edit SSH config or copy keys. The portal shows the current extension version and a VSIX download link in the top bar and in the **SSH** dialog; override `vscodeExtension.*` when you host releases elsewhere. The **PyCharm** button opens JetBrains Gateway with the generated `poddy-ssh-*` alias; Gateway must parse `~/.ssh/config` and uses `ProxyCommand` (not `ProxyJump`). The **JupyterLab** button opens the workspace port `8888` through the portal proxy; the image must run JupyterLab there. The **SSH** dialog remains the manual fallback, and a config saved only in WSL is still invisible to Windows editors.

**Short-lived certificates**: the portal exposes `POST /api/me/ssh-certificate` and signs certificates with the CA mounted from `ssh.ca`. Workspace `authorized_keys` includes the matching `cert-authority` line, so the same certificate authenticates to the bastion and to workspace `sshd`. The CA line is loopback-restricted only when `ssh.requireAgent=true`; with the port-forward fallback it is unrestricted. See [SSH access](ssh.md).

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

poddy supports the OpenID Connect authorization code flow with PKCE. When enabled, the login page shows **Continue with SSO**; sessions are stateless, HMAC-signed cookies; session state is not stored in PostgreSQL.

### Provider configuration

- **Redirect URI**: `https://<poddy-host>/oidc/callback`
- **Scopes**: `openid profile email groups` (adjust to your provider; `groups` is optional)
- **Client type**: confidential (with `clientSecret`) or public (PKCE, no secret)

### Helm values

```yaml
oidc:
  enabled: true
  issuer: https://keycloak.example.com/realms/dev
  # discoveryUrl: http://keycloak.internal/realms/dev
  # authorizationUrl: https://keycloak.example.com/realms/dev/protocol/openid-connect/auth
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
| `-oidc-issuer` | `PODDY_OIDC_ISSUER` | Issuer URL (the expected `iss` claim; enables OIDC). |
| `-oidc-discovery-url` | `PODDY_OIDC_DISCOVERY_URL` | Discovery URL when it differs from the issuer (defaults to issuer). |
| `-oidc-authorization-url` | `PODDY_OIDC_AUTHORIZATION_URL` | Browser-facing authorization endpoint override. |
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

`oidc.issuer`, `oidc.discoveryUrl`, `oidc.authorizationUrl`, and `oidc.redirectUrl` are independent. Use `discoveryUrl` when discovery must use an internal URL, and `authorizationUrl` when the browser needs a different authorization endpoint. See [OIDC / SSO](oidc.md#issuer-discovery-authorization-and-callback-urls).

## Chart publishing

The `Publish Helm charts` workflow pushes both charts to
`oci://ghcr.io/<owner>/charts` when chart files change on `main` or a `v*` tag
is pushed. On branches the chart version comes from `Chart.yaml`; on tags the
tag name without the leading `v` is used. Make the GHCR chart packages public
if unauthenticated users should install them.

## Upgrading

```bash
helm upgrade poddy oci://ghcr.io/slimaineb/charts/poddy \
  -n poddy-system --version 0.1.8 -f values.yaml
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