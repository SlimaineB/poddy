# PostgreSQL metadata and audit (optional)

!!! note "Optional mirror, Kubernetes-first"
    poddy runs without PostgreSQL. When `PODDY_DATABASE_URL` is set (Helm:
    `database.url` or `database.existingSecret`), the portal mirrors RBAC,
    OIDC users, templates, and workspaces into PostgreSQL and writes an
    append-only audit trail. Kubernetes remains the runtime source of truth;
    PostgreSQL is never read for workspace or RBAC decisions.

    The portal requires the database at startup when it is configured. If the
    database becomes unavailable later, sync and audit writes are logged and
    workspace operations continue. Fix the database and restart the portal to
    resynchronize the mirror.

PostgreSQL does **not** replace workspace PVCs. A database backup restores
control-plane metadata; workspace files still need volume snapshots or another
backup mechanism.

## Why add PostgreSQL?

Kubernetes is excellent at running workspaces, but it is not a durable backup of
its own metadata. If the cluster is lost, you lose the ConfigMaps and Secrets
that describe templates, RBAC, users, stopped workspaces, and external auth
connections.

A PostgreSQL metadata store gives you:

- A backup target that survives cluster loss.
- An audit trail for workspace lifecycle, terminal, VS Code, and SSH activity.
- A place for quotas and usage accounting.
- A shared catalog for multiple clusters or portals.
- A migration path toward a control plane that can recreate a cluster from
  metadata.

It does not give you:

- Workspace files, home directories, or PVC contents.
- Kubernetes credentials, OIDC sessions, or plaintext OAuth tokens.
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

## Schema migrations

By default the portal and bastion apply the embedded migrations on startup.
They use a PostgreSQL advisory lock and `poddy.schema_migrations`, so several
replicas can start at the same time safely.

To apply the migration manually instead:

```bash
psql --single-transaction -v ON_ERROR_STOP=1 \
  "$PODDY_DATABASE_URL" -f migrations/0001_init.sql
```

The automatic runner wraps each migration in a transaction. The migration
creates the `poddy` schema and records its version in `poddy.schema_migrations`.
Use `PODDY_DATABASE_AUTO_MIGRATE=false` after a manual migration if you do not
want the process to manage the schema.

## Tables

| Table | Purpose |
|-------|---------|
| `tenants` | One workspace namespace per tenant. |
| `users` | OIDC identities seen by the portal, including groups and first/last seen. |
| `global_admins` | Global admin identities. |
| `teams` | Named teams. |
| `team_members` | Team membership entries. |
| `templates` | Template catalog metadata and public/restricted state. |
| `template_versions` | Versioned template YAML, Pod template, parameters, links, and external auth requirements. |
| `template_owners` | Template owners. |
| `template_users` | Direct template users. |
| `template_teams` | Teams allowed to use a template. |
| `workspaces` | Workspace metadata and the rendered PodSpec. |
| `external_auth_tokens` | Reserved for an encrypted OAuth2 token mirror; not written by the portal yet. |
| `audit_events` | Append-only control-plane audit trail. |
| `schema_migrations` | Applied migration versions. |

## Mapping from Kubernetes

| Kubernetes object | PostgreSQL tables |
|-------------------|-------------------|
| `poddy-rbac` ConfigMap | `tenants`, `global_admins`, `teams`, `team_members`, `templates`, `template_owners`, `template_users`, `template_teams` |
| `poddy-users` ConfigMap | `users` |
| Template ConfigMaps | `templates`, `template_versions`, access tables |
| Workspace pods and stopped state | `workspaces` |
| `poddy-external-auth` Secret | `external_auth_tokens` (reserved; the portal currently leaves tokens in the Secret) |
| Audit stream | `audit_events` (written by portal and bastion) |

The mirror is refreshed at startup and after writes:

- Startup removes workspaces that no longer exist in Kubernetes; startup and
  catalog reloads remove stale templates, so rows deleted while the portal was
  down do not linger.
- RBAC and template access: after team or template-access changes.
- Templates: after template ConfigMaps or the local template directory reload.
- Users: when an OIDC user signs in.
- Workspaces: after create, stop, start, restart, and delete.
- Audit: HTTP actions through the portal and SSH connections through the bastion.

## Design notes

- **Namespace is the tenant boundary.** Every tenant-scoped table has a
  `namespace` column. Use one release per namespace for hard isolation.
- **Unversioned templates use `version = '0'`.** `template_versions` has a
  partial unique index so only one version per template can be active.
- **`templates.is_public` is derived.** A template with no access rows is
  public. When owners, users, or teams are assigned, set it to `false`.
- **Workspace template names are historical.** `workspaces.template_name` is
  kept even if the template is later deleted, so the workspace can still be
  recreated from its rendered spec.
- **`workspaces.rendered_pod` is JSONB.** It stores the rendered PodSpec so a
  stopped workspace can be recreated. It must not contain external auth tokens
  or Kubernetes credentials.
- **External auth tokens are reserved.** If token mirroring is added later,
  encrypt values in the application before writing. `encryption_key_version`
  identifies the key used for rotation.
- **No PVC contents.** `workspaces.storage` and `storage_class` are metadata
  only. Back up persistent volumes separately.
- **No plaintext secrets.** Passwords, session keys, SSH private keys, and
  OAuth tokens do not belong in this schema. The `external_auth_tokens` table is
  reserved for a future encrypted mirror; tokens currently remain in the
  Kubernetes Secret.
- **Audit writes are best effort.** The portal does not block a workspace
  operation if the audit insert fails, but it logs the error. Use Kubernetes
  audit logs as a second source when you need stronger guarantees.
- **Forward-only migrations.** Add new files as `migrations/0002_*.sql`; do not
  edit an applied migration.

## Audit actions

`audit_events` is append-only. The portal and bastion currently record:

| Area | Actions |
|------|---------|
| Authentication | `auth.login`, `auth.logout`, `auth.denied` |
| Workspaces | `workspace.create`, `workspace.start`, `workspace.stop`, `workspace.restart`, `workspace.delete`, `workspace.logs.view`, `workspace.vscode.open`, `workspace.terminal.open` |
| SSH | `ssh.connect` |
| Templates | `template.access.update` |
| Teams | `team.update`, `team.delete` |
| External auth | `external_auth.connect`, `external_auth.disconnect` |

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

1. **Mirror and audit (available now).** The portal writes Kubernetes metadata
   snapshots and audit events to PostgreSQL. Kubernetes remains authoritative.
2. **Reconcile (roadmap).** Add a restore command that recreates ConfigMaps and
   template objects from PostgreSQL in a new cluster.
3. **Source of truth (not planned by default).** poddy intentionally keeps
   Kubernetes as the control plane; a PostgreSQL-first mode would need a
   separate reconciliation design.

See [Data model](data-model.md) for the current Kubernetes objects and
[Kubernetes deployment](deploy.md) for backup guidance.
