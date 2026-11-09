# Data model

poddy does not require a database. By default, the portal reads and writes
Kubernetes objects in the workspace namespace. This page lists what is stored
where and which fields matter.

An optional PostgreSQL mirror can store RBAC, OIDC users, templates, workspaces,
and audit events. It is enabled with `PODDY_DATABASE_URL`; Kubernetes remains
the source of truth and PostgreSQL is never read for access decisions. See
[PostgreSQL schema](postgres-schema.md).

## Object map

| Data | Kubernetes object | Default name | Key |
|------|-------------------|--------------|-----|
| Running workspaces | Pods | `poddy-<workspace>` | labels + `poddy.io/owner` annotation |
| Persistent workspace data | PVCs | `poddy-<workspace>` | mounted at `/home/dev` |
| Stopped workspace specs | ConfigMap | `poddy-state` | `workspaces.yaml` |
| Templates | ConfigMaps | template name | `template.yaml`, `pod.yaml` |
| RBAC | ConfigMap | `poddy-rbac` | `rbac.yaml` |
| OIDC users | ConfigMap | `poddy-users` | `users.json` |
| Self-service SSH keys | ConfigMap | `poddy-user-ssh-keys` | `keys.json` |
| External auth tokens | Secret | `poddy-external-auth` | encrypted token document |
| Optional metadata mirror | PostgreSQL | `poddy` schema | `migrations/*.sql` |

The portal's Kubernetes Role is scoped to this namespace. It needs access to
pods, exec, port-forward, PVCs, ConfigMaps, and Secrets.

## Workspaces

A workspace is a pod with these labels:

```yaml
metadata:
  name: poddy-my-workspace
  labels:
    app.kubernetes.io/name: poddy
    app.kubernetes.io/managed-by: poddy
    poddy.io/workspace: my-workspace
    poddy.io/template: go
  annotations:
    poddy.io/owner: alice@example.com
    poddy.io/external-auth-env: '["GITHUB_TOKEN"]'
```

| Field | Meaning |
|-------|---------|
| `poddy.io/workspace` | Workspace name shown in the UI |
| `poddy.io/template` | Template used to create the workspace |
| `poddy.io/owner` | Authenticated owner; drives visibility and SSH checks |
| `poddy.io/state` | Original rendered pod spec, used when stopping |
| `poddy.io/external-auth-env` | Environment variables that receive refreshed tokens |

A persistent workspace gets a PVC named after the pod. Stopping a workspace
deletes the pod but keeps the PVC and writes the rendered spec to the state
ConfigMap. Optional volume backups are stored outside Kubernetes in
S3-compatible object storage as gzip tar archives. Backups can be taken from
running or stopped workspaces and restored into the same or another volume; see
[Volumes and backups](volumes.md).

## Stopped workspace state

The `poddy-state` ConfigMap stores a YAML document under `workspaces.yaml`:

```yaml
workspaces:
  my-workspace:
    name: my-workspace
    template: go
    owner: alice@example.com
    image: ghcr.io/slimaineb/poddy-workspace:latest
    persistent: true
    storage: 10Gi
    schedule:
      enabled: true
      timezone: Europe/Paris
      start: "09:00"
      stop: "18:00"
    spec:
      containers:
        - name: workspace
          image: ghcr.io/slimaineb/poddy-workspace:latest
```

This is enough to recreate the pod on **Start** even if the template catalog
changed in the meantime. The optional `schedule` is the workspace auto-stop
window; `stoppedBySchedule: true` marks a stop performed by the scheduler so a
manual stop is not restarted automatically.

## Templates

Templates are ConfigMaps labeled `poddy.io/template=true`. Each one has two
data keys:

| Key | Content |
|-----|---------|
| `template.yaml` | Metadata, image, parameters, links, external auth |
| `pod.yaml` | Partial Kubernetes pod overlay rendered with Go templates, then merged over poddy's defaults |

Versioning uses labels:

```yaml
metadata:
  labels:
    poddy.io/template: "true"
    poddy.io/template-name: go
    poddy.io/template-version: "2"
    poddy.io/template-active: "true"
```

Only the active version is served. Moving the active label is the rollback
mechanism. See [Workspace templates](templates.md) for the full contract.

## RBAC

The `poddy-rbac` ConfigMap stores `rbac.yaml`:

```yaml
admins:
  - admin@example.com
auditors:
  - security@example.com
teams:
  backend:
    members:
      - alice@example.com
      - bob@example.com
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
    users:
      - dave@example.com
    teams:
      - backend
```

| Role | Scope |
|------|-------|
| Global admin | Every workspace and template; manages teams and quotas |
| Auditor | Read-only access to the PostgreSQL audit trail |
| Template owner | Users and teams allowed to use their templates |
| Team member | Inherits access granted to the team and its quota defaults |
| Regular user | Own workspaces and templates assigned directly or by team |

A template with no access entry is public. The shared token and trusted local
mode act as global admins.

## OIDC users

The `poddy-users` ConfigMap stores `users.json`, a directory of identities seen
by the portal:

```json
{
  "users": {
    "alice@example.com": {
      "subject": "oidc-subject",
      "username": "alice",
      "email": "alice@example.com",
      "name": "Alice",
      "groups": ["backend"],
      "firstSeen": "2026-01-01T10:00:00Z",
      "lastSeen": "2026-01-02T10:00:00Z"
    }
  }
}
```

It is a convenience directory for the **Users** page, not the authentication
source. OIDC remains the identity provider.

## External auth tokens

The `poddy-external-auth` Secret stores per-user OAuth2 tokens for providers
such as GitHub, GitLab, Gitea, Azure DevOps, and Bitbucket. Tokens are encrypted
with AES-GCM when `PODDY_EXTERNAL_AUTH_KEY` is set.

Required providers block workspace creation until the user connects them.
Optional providers are injected only when connected. Workspace pods receive
refreshed tokens through environment variables declared by the template.

## Optional PostgreSQL metadata

When `PODDY_DATABASE_URL` is set, the portal mirrors the same metadata into
PostgreSQL: tenants, users, teams, team/user quotas, RBAC, templates, template
versions, workspaces, and audit events. The bastion also writes SSH audit
events. The portal syncs at startup and after writes; it does not read
PostgreSQL for RBAC, quota, or workspace decisions.

The `external_auth_tokens` table is reserved for a future encrypted mirror;
tokens currently remain in the Kubernetes Secret. PostgreSQL never stores PVC
contents or plaintext secrets.

See [PostgreSQL schema](postgres-schema.md) for the table list, audit actions,
backup strategy, and current limitations.

## What is not stored

- No database is required at runtime.
- No workspace files or PVC contents in PostgreSQL.
- No PostgreSQL reads for access or quota decisions; Kubernetes remains
  authoritative.
- No plaintext OAuth tokens in PostgreSQL.
- No long-lived Kubernetes credentials in the browser.
- No cluster-wide access for the portal.
- No shared home volumes across users unless you configure them yourself.

See [Architecture](architecture.md) for the request flows and
[RBAC and ownership](rbac.md) for access rules.
