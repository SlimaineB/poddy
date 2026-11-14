# Data model

poddy does not require a database. By default, the portal reads and writes
Kubernetes objects in the workspace namespace. This page lists what is stored
where and which fields matter.

An optional PostgreSQL database can store the control plane. It is enabled with
`PODDY_DATABASE_URL`. When configured, PostgreSQL is authoritative for RBAC,
OIDC users, external auth tokens, share links, access state, and quotas. The
`poddy-rbac` and `poddy-users` ConfigMaps become materialized mirrors. Templates
and workspaces remain Kubernetes-first and are mirrored for audit and recovery.
See [PostgreSQL schema](postgres-schema.md).

## Object map

| Data | Kubernetes object | Default name | Key |
|------|-------------------|--------------|-----|
| Workspaces | StatefulSets | `poddy-<workspace>` | pod template + `poddy.io/owner` annotation |
| Persistent workspace data | PVCs | `poddy-<workspace>` | mounted at `/home/dev` |
| Legacy stopped workspace specs | ConfigMap | `poddy-state` | `workspaces.yaml` (migration only) |
| Templates | ConfigMaps | template name | `template.yaml`, `pod.yaml` |
| RBAC (Kubernetes fallback or PostgreSQL mirror) | ConfigMap | `poddy-rbac` | `rbac.yaml` |
| OIDC users (Kubernetes fallback or PostgreSQL mirror) | ConfigMap | `poddy-users` | `users.json` |
| Self-service SSH keys | ConfigMap | `poddy-user-ssh-keys` | `keys.json` |
| Workspace SSH keys | Secret | `poddy-<workspace>-ssh-keys` | `authorized_keys` |
| Workspace share links (Kubernetes fallback or migration source) | Secret | `poddy-shares` | `shares.json` |
| Agent tunnel key | Secret | `<release>-agent` | `agent-key` |
| SSH certificate authority | Secret | `<release>-ssh-ca` | `ssh-ca-key` |
| External auth tokens (Kubernetes fallback or migration source) | Secret | `poddy-external-auth` | encrypted token document |
| PostgreSQL control plane and metadata mirror | PostgreSQL | `poddy` schema | `migrations/*.sql` |

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
| `poddy.io/state` | Legacy rendered pod spec on bare pods; removed when adopting into a StatefulSet |
| `poddy.io/external-auth-env` | Environment variables that receive refreshed tokens |

A persistent workspace gets a PVC named `poddy-<workspace>`. Stopping a
workspace scales its StatefulSet to zero but keeps the PVC and the rendered pod
template. Optional volume backups are stored outside Kubernetes in
S3-compatible object storage as gzip tar archives. Backups can be taken from
running or stopped workspaces and restored into the same or another volume; see
[Volumes and backups](volumes.md).

## Workspace state

Each workspace is one StatefulSet named `poddy-<workspace>`. The StatefulSet is
the source of truth for both running and stopped workspaces:

- `replicas: 1` means running; the controller creates pod `poddy-<workspace>-0`.
- `replicas: 0` means stopped; the pod template and annotations are retained.
- The separate PVC `poddy-<workspace>` keeps `/home/dev`.

The template carries the rendered pod spec. Metadata annotations carry the
owner, template version, parameters, storage request, schedule, and stop state
(`poddy.io/stopped-at`, `poddy.io/stopped-by-schedule`). Stopping strips
dynamic credentials (external auth environment and agent token) from the
template; starting refreshes them before scaling to one. The legacy
`poddy-state` ConfigMap is read at startup to create stopped StatefulSets; running bare pods migrate on their next stop.

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

When `PODDY_DATABASE_URL` is set, PostgreSQL stores RBAC (`rbac_documents`),
OIDC users (`users`), encrypted external auth tokens (`external_auth_tokens`),
workspace share links (`share_links`), access state, quotas, templates,
workspace metadata, and audit events. The portal writes RBAC and users to
PostgreSQL, then reconciles `poddy-rbac` and `poddy-users` as best-effort
ConfigMap mirrors. External auth and share writes go only to PostgreSQL after
the first import; their old Secrets are migration sources and are no longer
updated.

Share lookup uses a SHA-256 token hash and stores the raw token AES-GCM
encrypted so existing share URLs can be rebuilt. External auth access and
refresh tokens are also AES-GCM encrypted. Set a stable
`PODDY_EXTERNAL_AUTH_KEY`, `PODDY_OIDC_SESSION_KEY`, or `PODDY_DESKTOP_SECRET`
to keep tokens and shares valid across restarts. Without a stable key, external
auth uses an ephemeral key and share links stay in Kubernetes. PostgreSQL never
stores PVC contents or plaintext secrets.

See [PostgreSQL schema](postgres-schema.md) for the table list, audit actions,
backup strategy, and current limitations.

## What is not stored

- No database is required at runtime; Kubernetes is the fallback.
- No workspace files or PVC contents in PostgreSQL.
- No PostgreSQL-first mode for workspaces or templates; Kubernetes remains
  their runtime source of truth.
- No plaintext OAuth or share tokens in PostgreSQL.
- No long-lived Kubernetes credentials in the browser.
- No cluster-wide access for the portal.
- No shared home volumes across users unless you configure them yourself.

See [Architecture](architecture.md) for the request flows and
[RBAC and ownership](rbac.md) for access rules.
