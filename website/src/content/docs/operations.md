---
title: "Operations"
description: "Day-two runbook for upgrades, backups, restore, monitoring, scaling, and routine security work."
---

This is the day-2 runbook for a poddy installation: upgrades, backups, restore,
monitoring, scaling, and routine security work. For first-time production
values, read [Kubernetes deployment](deploy.md) first.

## What must be backed up

poddy has three kinds of persistent state. Back them up independently.

| State | Where it lives | How to protect it |
|-------|----------------|-------------------|
| Control plane | PostgreSQL | Logical database backups, tested restores, managed snapshots. |
| Workspace files | PVCs in the workspace namespace | Volume backups to S3, storage-class snapshots, or both. |
| Cryptographic keys | Kubernetes Secrets | Encrypted secret backups in a separate trust domain. |

### Critical Secrets

Losing these breaks access or makes stored data unreadable:

- `poddy-auth`: the shared token, if you still use one.
- `poddy-oidc`: OIDC client secret and session key. A changed session key
  invalidates all browser sessions.
- The external-auth/share/environment encryption key
  (`PODDY_EXTERNAL_AUTH_KEY`, `PODDY_OIDC_SESSION_KEY`, or
  `PODDY_DESKTOP_SECRET` as configured). If no key is set, the database URL is
  the fallback key; keep it stable.
- `poddy-ssh-ca`: the SSH certificate authority.
- Your SSH host key Secret (`ssh.existingHostKeySecret`), if configured. A
  stable host key is strongly recommended for production and required for
  multiple bastion replicas.
- The agent HMAC key (`poddy-agent` or `ssh.agent.existingSecret`).
- `poddy-regions`: region keys on the hub.
- `poddy-backup`: S3 credentials, if poddy manages backups.
- Any registry pull Secrets used by workspaces.

Store copies encrypted, access-controlled, and outside the cluster. A database
backup without the encryption key is not a usable restore.

## Back up PostgreSQL

Use a transactionally consistent logical dump. Adjust the connection details for
your database.

```bash
kubectl -n poddy-system port-forward svc/poddy-postgres-postgresql 5432:5432
pg_dump --format=custom --no-owner --no-acl \
  --file=poddy-$(date +%F).dump \
  'postgres://poddy:password@127.0.0.1:5432/poddy?sslmode=disable'
```

For a managed database, prefer the provider's snapshot plus a logical dump. Keep
at least one dump outside the primary region.

Verify backups regularly:

```bash
createdb poddy_restore_test
pg_restore --clean --if-exists --no-owner --no-acl \
  --dbname=poddy_restore_test poddy-2026-01-01.dump
psql -d poddy_restore_test -c 'select count(*) from poddy.users;'
```

### Restore PostgreSQL

1. Stop portal and bastion traffic, or scale both Deployments to zero.
2. Restore the database into an empty database.
3. Restore the matching encryption, OIDC, SSH CA, host, agent, and region
   Secrets.
4. Start the portal and check migrations and login.
5. Start the bastion and verify SSH.
6. Reconcile templates and workspaces from Kubernetes if needed.

Application rollback does not reverse database migrations. Restore the database
backup that matches the application version you intend to run.

## Back up workspace volumes

Enable S3 backups when you need self-service backup and restore from the
**Volumes** page:

```yaml
backup:
  enabled: true
  helperImage: docker.io/library/busybox:1.36
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

Requirements:

- the bucket must exist;
- credentials need list/get/put/delete on the configured prefix;
- the region manager needs network access to the endpoint;
- the volume must be `Bound`.

A live backup is crash-consistent at best. Stop the workspace first if files
must be consistent. Restore requires a **Stopped** workspace. Delete the
workspace first if you want to restore into a new volume.

For clusters with storage snapshots, also schedule `VolumeSnapshot` objects for
PVCs labeled `app.kubernetes.io/name=poddy`. Object-level and snapshot backups
protect against different failures.

## Upgrade

### Before the upgrade

1. Read the release notes and chart changes.
2. Back up PostgreSQL and the critical Secrets.
3. Confirm the new image and chart versions exist in your registry.
4. Check the Kubernetes version and any changed values.
5. Schedule a window if the upgrade changes the agent sidecar or SSH CA.

### Upgrade the charts

```bash
helm repo update 2>/dev/null || true

helm upgrade poddy oci://ghcr.io/slimaineb/charts/poddy \
  -n poddy-system --version <new-version> -f values.yaml \
  --wait --timeout 10m

helm upgrade poddy-templates oci://ghcr.io/slimaineb/charts/poddy-templates \
  -n poddy-workspaces --version <new-version> \
  --wait --timeout 5m
```

If the chart and image versions are independent, update both:

```bash
helm upgrade poddy oci://ghcr.io/slimaineb/charts/poddy \
  -n poddy-system --version <new-version> \
  --reuse-values \
  --set image.tag=<new-image-tag> \
  --wait --timeout 10m
```

### After the upgrade

```bash
kubectl -n poddy-system rollout status deploy/poddy --timeout=5m
kubectl -n poddy-system rollout status deploy/poddy-bastion --timeout=5m
kubectl -n poddy-system get pods
kubectl -n poddy-workspaces get statefulsets,pods,pvc
```

Then verify:

- sign-in with OIDC and with the shared token, if enabled;
- workspace list and details;
- creating a workspace from each important template;
- browser terminal, VS Code Web, and SSH;
- external-auth token refresh;
- a volume backup or restore;
- audit events.

### Workspace changes after upgrade

- **Agent sidecar changes**: existing workspaces need a stop/start to receive
  the new sidecar. Stop and start them during a maintenance window.
- **SSH CA or `ssh.requireAgent` changes**: the CA line and agent settings are
  applied when a workspace is created or restarted. Restart existing workspaces.
- **Template changes**: stopped workspaces keep their preserved pod template.
  Delete and recreate a workspace to adopt template changes.
- **Agent tokens**: workspace tokens default to one hour. The control plane
  refreshes the per-workspace Secret at half the token lifetime, the agent
  re-reads the mounted Secret before reconnecting, and an established tunnel
  renews tokens in place. Rotate the `ssh.agent` Secret to revoke all agent
  tokens.

### Roll back

```bash
helm history poddy -n poddy-system
helm rollback poddy <revision> -n poddy-system
```

Rollback is safest when the database schema did not change. If migrations ran,
restore the matching PostgreSQL backup and the matching Secrets instead of
assuming the old binary can read the new schema.

## Monitor

The portal exposes Prometheus metrics on `:9090` by default in the chart. Keep
the metrics Service internal; it has no authentication.

Useful metrics:

| Metric | Use |
|--------|-----|
| `poddy_http_requests_total` | Request volume and 4xx/5xx rate by route. |
| `poddy_http_request_duration_seconds` | Portal latency, including terminal and IDE proxies. |
| `poddy_http_in_flight_requests` | Concurrency and saturation. |
| `poddy_workspace_operations_total` | Create/start/stop/delete failures by result. |
| `poddy_workspace_create_duration_seconds` | Slow image pulls, scheduling, or API throttling. |
| `poddy_workspaces` | Workspaces by phase and template. |
| `poddy_auth_attempts_total` | Failed logins or OIDC problems. |
| `poddy_template_reloads_total` | Invalid template updates. |
| `poddy_build_info` | Deployed version. |

Suggested alerts:

- portal 5xx rate above 1% for 10 minutes;
- p95 portal latency above 2 seconds for 10 minutes;
- `poddy_workspaces{phase="Pending"}` growing;
- repeated `poddy_workspace_operations_total{result!="success"}`;
- PostgreSQL unavailable, replication lag, or disk above 80%;
- PVC usage above 85%;
- bastion restarts or failed SSH authentication spikes;
- region tunnel disconnected for more than 5 minutes;
- certificate or token expiration approaching.

In-product monitoring is separate from the `/metrics` listener. If enabled, the
portal reads live usage from `metrics-server` and history from Prometheus. Check
`GET /api/config` (`monitoring.history` is false when Prometheus is not
configured) and the workspace **Monitoring** panel. Common failures are missing
`metrics.k8s.io` RBAC, an unreachable Prometheus URL, or Prometheus without the
cAdvisor/kube-state-metrics series. Global history only queries workspace pods
matching `poddy-*-0`.

The **FinOps** page (and `GET /api/monitoring/finops`) compares requested CPU
and memory with live usage for every workspace. It flags idle, oversized,
contended, restarting, and OOM-killed workloads. Resource waste needs no
configuration; adding prices turns it into estimated hourly and monthly spend.

```yaml
monitoring:
  costs:
    enabled: true       # PODDY_MONITORING_COSTS
    visibleToUsers: false # PODDY_MONITORING_COSTS_FOR_USERS
    cpuCoreHour: 0.04     # PODDY_COST_CPU_CORE_HOUR
    memoryGBHour: 0.005   # PODDY_COST_MEMORY_GB_HOUR
    storageGBMonth: 0.08  # PODDY_COST_STORAGE_GB_MONTH
    currency: USD         # PODDY_COST_CURRENCY
```

Costs stay hidden unless pricing is configured **and** `monitoring.costs` is
enabled. Admins always see spend; non-admins see it only when
`costsForUsers` is also true. Per-member waste is shown to admins only, with
OIDC group membership resolved from each user's last sign-in. On-prem clusters
without meaningful prices can leave pricing empty and use resource waste only.

Also log:

- portal and bastion JSON logs;
- Kubernetes events in `poddy-system` and the workspace namespace;
- audit events in the portal **Audit** page or PostgreSQL;
- ingress and load-balancer errors for WebSocket upgrades.

## Scale and high availability

- **Portal replicas**: stateless for HTTP if every replica shares the OIDC session
  key and the external-auth/share/environment encryption key. When regions are
  configured, keep the hub at one replica or use sticky routing because region
  tunnel sessions live in the hub process.
- **Bastion replicas**: set `bastion.replicaCount` and provide a shared SSH host
  key with `ssh.existingHostKeySecret`. Otherwise host-key warnings appear after
  scaling or restarts. On shutdown, the bastion keeps existing SSH sessions for
  `bastion.drainTimeout`; keep `bastion.terminationGracePeriodSeconds` above it.
- **PDBs**: enable `podDisruptionBudget` and
  `bastion.podDisruptionBudget` when running more than one replica.
- **Database**: use a managed or HA PostgreSQL, not a single demo pod. Size
  connections for the portal and bastion replicas.
- **Kubernetes client**: the default is 50 QPS / 100 burst. Raise
  `kubernetes.qps`/`kubernetes.burst` if many users open terminals or browser
  IDEs at once.
- **Namespaces**: use one release and workspace namespace per team for hard
  isolation, or one portal with a dedicated workspace namespace. There is no
  automatic multi-namespace tenancy inside one release.

See [Kubernetes deployment](deploy.md) for the capacity guidance and HA state
matrix.

## Security maintenance

- Disable the shared token (`auth.tokenAdmin: false`) and use OIDC for a team.
- Rotate the shared token by updating `poddy-auth` and restarting the portal.
- Rotate OIDC client secrets and the session key during a maintenance window;
  sessions are invalidated when the session key changes.
- Rotate region keys by updating the hub and region Secrets, then restarting the
  region Deployment.
- Rotate the SSH CA carefully. New and restarted workspaces trust the new CA;
  existing workspaces keep the old CA line until restarted.
- Rotate the bastion host key only with a planned user notification, because
  clients will see a host-key change.
- Keep `ssh.authorizedKeys` break-glass admin keys few and audited.
- Keep `metrics`, PostgreSQL, and the workspace namespace off the public
  internet.
- Add NetworkPolicies for the portal, bastion, database, and workspace
  namespace.
- Review global admins, team membership, template access, and owner approvals
  quarterly.
- Patch the portal image, base workspace images, PostgreSQL, and ingress
  controller on a regular cadence.
- Back up and test the restore path, not just the backup job.

## Region operations

The hub routes workspace operations to each region over an outbound tunnel. For
each region:

- monitor tunnel reconnects and `502` responses;
- keep the same SSH CA available to the hub and every region bastion;
- back up each region's workspace PVCs;
- keep the hub at one replica or use sticky routing;
- test workspace creation in every region after an upgrade.

See [Regions](regions.md) for the full checklist.

## Uninstall and data retention

```bash
helm -n poddy-workspaces uninstall poddy-templates
helm -n poddy-system uninstall poddy
```

`helm uninstall` removes the workloads and chart-managed objects. It does not
remove data stored outside the release:

- PostgreSQL rows and backups;
- PVCs in the workspace namespace;
- S3 backup objects;
- Secrets you created manually.

:::danger[Workspace namespace deletion]
If the portal created the workspace namespace with
`createWorkspaceNamespace=true`, uninstalling the portal can delete that
namespace and every workspace PVC inside it. In production, create the
namespace yourself and set `createWorkspaceNamespace: false`.
:::

To remove everything, delete the workspace PVCs and namespace, drop the
database, and remove the S3 backup prefix deliberately.

## Incident checklist

1. Check `kubectl -n poddy-system get pods` and recent events.
2. Check portal logs for database, OIDC, Kubernetes API, or encryption errors.
3. Check PostgreSQL availability, disk, connections, and migrations.
4. Check the workspace namespace for pending pods, PVCs, and image pulls.
5. Check the ingress/load balancer for WebSocket upgrades and TLS.
6. For SSH, check the bastion Deployment, host key Secret, agent connectivity,
   and CA Secret.
7. For login, check the OIDC issuer, callback URL, clock skew, and group claims.
8. After recovery, verify workspace creation, terminal, SSH, and a backup.

See [Troubleshooting](troubleshooting.md) for symptom-specific steps.
