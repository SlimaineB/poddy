# PostgreSQL metadata and audit (optional)

!!! note "Optional PostgreSQL, Kubernetes fallback"
    poddy runs without PostgreSQL. When `PODDY_DATABASE_URL` is set (Helm:
    `database.url` or `database.existingSecret`), PostgreSQL becomes the
    authoritative control-plane store for RBAC, OIDC users, encrypted external
    auth tokens, workspace share links, workspace access, and quotas. The portal
    keeps `poddy-rbac` and `poddy-users` ConfigMaps as materialized mirrors, and
    writes an append-only audit trail. Kubernetes remains the runtime source of
    truth for workspaces and templates.

    Without a database, RBAC, users, shares, and external auth tokens stay in
    Kubernetes objects, and access/quotas use an in-process store that only
    works for a single replica.

    In a multi-region deployment only the hub connects to PostgreSQL. Region
    portals and bastions call the hub's internal control-plane endpoints
    (`/api/internal/region/access` and `/api/internal/region/quotas`) with their
    region key, so they never need database credentials or network access.

    The portal retries the database at startup when it is configured. If it is
    still unavailable, the portal starts with the Kubernetes and in-process
    fallbacks. If the database becomes unavailable later, metadata and audit
    writes are logged and workspace operations continue; RBAC, user, share, and
    token reads fail until PostgreSQL is reachable again. Restart the portal to
    re-enable PostgreSQL-first stores after a startup fallback.

PostgreSQL does **not** replace workspace PVCs. A database backup restores
control-plane metadata; workspace files still need volume snapshots or another
backup mechanism.

## Why add PostgreSQL?

Kubernetes is excellent at running workspaces, but it is not a durable backup of
its own metadata. If the cluster is lost, you lose the ConfigMaps and Secrets
that describe templates, RBAC, users, stopped workspaces, external auth
connections, and share links. With PostgreSQL configured, those control-plane
records are stored in the database and Kubernetes objects become mirrors or
runtime inputs.

A PostgreSQL metadata store gives you:

- A backup target that survives cluster loss.
- An audit trail for workspace lifecycle, terminal, VS Code, and SSH activity.
- A durable store for team and user quota configuration. The portal reads these
  tables when it enforces quotas; the RBAC ConfigMap no longer holds quotas.
- A shared catalog for multiple clusters or portals.
- A PostgreSQL-first store for RBAC, OIDC users, external auth tokens, and share
  links, plus a migration path toward recreating a cluster from metadata.

It does not give you:

- Workspace files, home directories, or PVC contents.
- Kubernetes credentials, OIDC sessions, plaintext OAuth tokens, or plaintext
  share tokens.
- A replacement for a GitOps workflow for templates and secrets.

## Enable it

Use PostgreSQL 13 or newer. A managed external database is recommended.

With Helm, either set the connection string directly:

```yaml
database:
  url: postgres://poddy:secret@postgres.example.com:5432/poddy?sslmode=require
  autoMigrate: true
```

Or reference a Secret in the release namespace:

```yaml
database:
  existingSecret: poddy-database
  urlKey: database-url
  autoMigrate: true
```

Create that Secret with the connection string:

```bash
kubectl -n poddy-system create secret generic poddy-database \
  --from-literal=database-url='postgres://poddy:secret@postgres.example.com:5432/poddy?sslmode=require'
```

The chart injects `PODDY_DATABASE_URL` into the portal and bastion. The portal
mirrors metadata and audits HTTP actions; the bastion audits SSH connections.
Set `database.autoMigrate: false` (or `PODDY_DATABASE_AUTO_MIGRATE=false`) when
migrations are managed externally.

For local development, set the variables directly:

```bash
export PODDY_DATABASE_URL='postgres://postgres:test@127.0.0.1:5432/poddy?sslmode=disable'
export PODDY_DATABASE_AUTO_MIGRATE=true
```

## PostgreSQL on EFS

If you run PostgreSQL on an EFS-backed volume, set `PGDATA` to a subdirectory.
EFS creates a `lost+found` directory at the volume root, and PostgreSQL refuses
to initialize a data directory that is not empty:

```yaml
env:
  - name: PGDATA
    value: /var/lib/postgresql/data/pgdata
volumeMounts:
  - name: postgres-data
    mountPath: /var/lib/postgresql/data
```

The same applies to any shared filesystem that creates `lost+found` at the
mount root. For production, a managed database or an EBS-backed volume is
usually a better choice than EFS for PostgreSQL.

## Schema migrations

By default the portal and bastion apply the embedded migrations on startup.
They use a PostgreSQL advisory lock and `poddy.schema_migrations`, so several
replicas can start at the same time safely.

To apply the migrations manually instead, apply every file in lexical order
(`0001_init.sql`, then `0002_quotas.sql`, and so on) and record the same version
rows that the automatic runner would write. The runner wraps each file in a
transaction and records its version (the filename without `.sql`) in
`poddy.schema_migrations`. It re-runs any file without a matching row, so do
not disable auto-migration halfway through a manual application.

```bash
for file in migrations/*.sql; do
  psql --single-transaction -v ON_ERROR_STOP=1 "$PODDY_DATABASE_URL" -f "$file"
  psql -v ON_ERROR_STOP=1 "$PODDY_DATABASE_URL" \
    -c "INSERT INTO poddy.schema_migrations (version) VALUES ('$(basename "$file" .sql)') ON CONFLICT DO NOTHING"
done
```

Use `PODDY_DATABASE_AUTO_MIGRATE=false` only after the schema and
`poddy.schema_migrations` are fully up to date. If migrations run as a separate
owner role, grant the portal role `SELECT`, `INSERT`, `UPDATE`, and `DELETE` on
the new `poddy.rbac_documents` and `poddy.share_links` tables.

## Tables

| Table | Purpose |
|-------|---------|
| `tenants` | One workspace namespace per tenant. |
| `users` | Authoritative OIDC identities, groups, and first/last seen. |
| `global_admins` | Global admin identities. |
| `teams` | Named teams (membership lives in `team_members`). |
| `team_members` | Team membership entries. |
| `user_quotas` | Per-user quota overrides resolved against team defaults. |
| `team_quotas` | Per-team quota defaults inherited by members. |
| `templates` | Template catalog metadata and public/restricted state. |
| `template_versions` | Versioned template YAML, Pod template, parameters, links, and external auth requirements. |
| `template_owners` | Template owners. |
| `template_users` | Direct template users. |
| `template_teams` | Teams allowed to use a template. |
| `workspaces` | Workspace metadata and the rendered PodSpec. |
| `access_requests` | Pending admin access requests awaiting owner approval. |
| `access_grants` | Time-limited owner approvals for interactive access. |
| `access_presence` | Active interactive sessions with a heartbeat and TTL. |
| `external_auth_tokens` | AES-GCM encrypted OAuth2 access and refresh tokens per user/provider. |
| `rbac_documents` | Authoritative RBAC YAML/JSON document per namespace. |
| `share_links` | Workspace share links with hashed lookup tokens and encrypted raw tokens. |
| `metadata_imports` | One-time Kubernetes Secret import markers for external auth tokens and shares. |
| `audit_events` | Append-only control-plane audit trail. |
| `schema_migrations` | Applied migration versions. |

## Mapping from Kubernetes

| Kubernetes object | PostgreSQL tables | Direction |
|-------------------|-------------------|-----------|
| `poddy-rbac` ConfigMap | `rbac_documents` plus normalized `global_admins`, `teams`, `team_members`, template access rows | PostgreSQL first; ConfigMap is the materialized mirror |
| `poddy-users` ConfigMap | `users` | PostgreSQL first; ConfigMap is the materialized mirror |
| Template ConfigMaps | `templates`, `template_versions`, access tables | Kubernetes first; PostgreSQL mirror |
| Workspace StatefulSets and stopped state | `workspaces` | Kubernetes first; PostgreSQL mirror |
| `poddy-external-auth` Secret | `external_auth_tokens` | Migration source only after PostgreSQL is enabled |
| `poddy-shares` Secret | `share_links` | Migration source only after PostgreSQL is enabled |
| Audit stream | `audit_events` | Written by portal and bastion |

Workspace access state (`access_requests`, `access_grants`, `access_presence`)
and quotas (`user_quotas`, `team_quotas`) are owned by PostgreSQL rather than
mirrored from Kubernetes. The hub portal and bastion read and write them
directly; regions go through the hub control plane. There is no Kubernetes
Secret for access and no quota entry in the RBAC ConfigMap.

PostgreSQL-first stores reconcile as follows:

- On first use, an empty `rbac_documents`, `users`, `external_auth_tokens`, or
  `share_links` table is seeded from the corresponding Kubernetes object.
- RBAC and user writes go to PostgreSQL, then update `poddy-rbac` and
  `poddy-users` after commit on a best-effort basis. The ConfigMaps remain
  readable for GitOps, debugging, and rollback to a Kubernetes-backed release.
- External auth and share writes go to PostgreSQL only. The old Secret is left
  untouched as a migration source but is no longer updated. `metadata_imports`
  records that the one-time import happened, so deleting all tokens or shares
  does not re-import old values from the Secret after a restart.
- Startup removes workspaces that no longer exist in Kubernetes; startup and
  catalog reloads remove stale templates, so rows deleted while the portal was
  down do not linger.
- Templates remain Kubernetes-first and are mirrored after ConfigMap or local
  directory reloads. Workspaces are mirrored after create, stop, start, restart,
  and delete. Audit events are written as HTTP and SSH actions occur.
- Regions still call the hub control plane for access and quotas. A region with
  its own database uses local PostgreSQL-first stores; without one it keeps the
  Kubernetes fallback.

## Audit read API and UI

When PostgreSQL is configured, admins and identities listed in `rbac.auditors`
can read the audit trail from the **Audit** page or directly:

```http
GET /api/audit?outcome=denied&action=workspace.create&since=2026-01-01&page=1&per_page=50
```

Supported query parameters:

| Parameter | Meaning |
|-----------|---------|
| `q` | Case-insensitive search across actor, action, target, workspace, and details. |
| `action` | Exact action, for example `workspace.create`. |
| `outcome` | `success`, `denied`, or `error`. |
| `actor` | Exact actor identity. |
| `workspace` | Exact workspace name. |
| `since` / `until` | RFC3339 timestamp or `YYYY-MM-DD`. `until` is exclusive for date-only values. |
| `page` / `per_page` | Pagination; `per_page` is capped at 100. |

The endpoint returns `403` for other users and `501` when PostgreSQL is not
configured. Reading the audit trail itself is recorded as `audit.view`.
Auditors can only read; all mutations still require an admin or the workspace
owner as appropriate.

## Design notes

- **Namespace is the tenant boundary.** Every tenant-scoped table has a
  `namespace` column. Use one release per namespace for hard isolation.
- **Unversioned templates use `version = '0'`.** `template_versions` has a
  partial unique index so only one version per template can be active.
- **`templates.is_public` is derived.** A template with no access rows is
  public. When owners, users, or teams are assigned, set it to `false`.
- **Quotas are configuration, not accounting.** `team_quotas` and `user_quotas`
  are the enforcement source. The portal computes live usage from Kubernetes
  workspaces and PVCs, then checks it against the effective quota (user
  overrides fill team defaults field by field).
- **Workspace template names are historical.** `workspaces.template_name` is
  kept even if the template is later deleted, so the workspace can still be
  recreated from its rendered spec.
- **`workspaces.rendered_pod` is JSONB.** It stores the rendered PodSpec so a
  stopped workspace can be recreated. It must not contain external auth tokens
  or Kubernetes credentials.
- **External auth tokens are encrypted in the application.** Access and refresh
  tokens are AES-GCM encrypted before the insert; `encryption_key_version`
  identifies the key used for rotation.
- **Share tokens are hashed and encrypted.** `share_links.token_hash` is the
  SHA-256 lookup key. `token_ciphertext` is the AES-GCM encrypted raw token, kept
  only so the portal can rebuild the existing share URL. Never expose either
  column through the API or logs.
- **Use a stable encryption secret.** External token and share encryption use
  `PODDY_EXTERNAL_AUTH_KEY`, then `PODDY_OIDC_SESSION_KEY`, then
  `PODDY_DESKTOP_SECRET`. If none is set, external auth uses an ephemeral key and
  share links stay in Kubernetes instead of switching to PostgreSQL.
- **No PVC contents.** `workspaces.storage` and `storage_class` are metadata
  only. Back up persistent volumes separately.
- **No plaintext secrets.** Passwords, session keys, SSH private keys, OAuth
  tokens, and share tokens do not belong in this schema in plaintext. OAuth and
  share tokens are encrypted by the application before insertion.
- **Audit writes are best effort.** The portal does not block a workspace
  operation if the audit insert fails, but it logs the error. Use Kubernetes
  audit logs as a second source when you need stronger guarantees.
- **Forward-only migrations.** Add new files as `migrations/0005_*.sql`; do not
  edit an applied migration.

## Audit actions

`audit_events` is append-only. The portal and bastion currently record:

| Area | Actions |
|------|---------|
| Authentication | `auth.login`, `auth.logout`, `auth.denied` |
| Workspaces | `workspace.create`, `workspace.start`, `workspace.stop`, `workspace.restart`, `workspace.delete`, `workspace.logs.view`, `workspace.vscode.open`, `workspace.cursor.open`, `workspace.gateway.open`, `workspace.desktop.open`, `workspace.terminal.open`, `workspace.share.update` |
| SSH | `ssh.connect` |
| Templates | `template.access.update` |
| Teams | `team.update`, `team.delete` |
| User quotas | `user_quota.update`, `user_quota.delete` |
| External auth | `external_auth.connect`, `external_auth.disconnect` |
| Audit | `audit.view` |

Use `outcome = 'denied'` for authorization failures and `outcome = 'error'` for
failed operations. `details` contains non-secret context such as the requested
template. Tokens and secrets are never written to the audit row.

For high-volume deployments, partition `audit_events` by month and set a
retention policy (for example 90 days online, 1 year in cold storage).

## Backup and restore

Take logical backups regularly and test a restore into an empty database:

```bash
pg_dump --format=custom --no-owner --no-privileges \
  "$PODDY_DATABASE_URL" > poddy.dump

createdb poddy_restore
pg_restore --clean --if-exists --no-owner --no-privileges \
  -d postgresql:///poddy_restore poddy.dump
```

For production, also enable:

- Point-in-time recovery (PITR) on the managed database.
- Encryption at rest and TLS in transit.
- A separate database user for poddy with access only to the `poddy` schema.
- A restore test that recreates a workspace from `workspaces.rendered_pod`.
- PVC snapshots or a storage-level backup for workspace data.

## Current behavior and next steps

1. **PostgreSQL-first control plane (available now).** RBAC, OIDC users,
   external auth tokens, share links, access state, and quotas live in
   PostgreSQL when a database is configured. The `poddy-rbac` and
   `poddy-users` ConfigMaps are reconciled mirrors; templates and workspaces
   remain Kubernetes-first and are mirrored for audit and recovery.
2. **Kubernetes fallback (available now).** Without a database, RBAC, users,
   shares, and external auth tokens stay in Kubernetes. Access and quotas use
   the in-process fallback and should stay at one portal replica.
3. **Restore (roadmap).** Add a command that recreates the mirror ConfigMaps,
   templates, and workspace StatefulSets from PostgreSQL in a new cluster.

See [Data model](data-model.md) for the current Kubernetes objects and
[Kubernetes deployment](deploy.md) for backup guidance.
