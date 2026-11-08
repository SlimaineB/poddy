# RBAC: admins, owners, and teams

poddy uses a deliberately small RBAC model stored in a Kubernetes ConfigMap. No database is required. An optional PostgreSQL mirror can copy RBAC metadata for disaster recovery and audit; Kubernetes remains authoritative. See [PostgreSQL schema](postgres-schema.md).

- **Global admins** see and manage every workspace and template.
- **Template owners** manage who can use their templates.
- **Teams** are named groups of users that can be assigned to templates.
- **Regular users** see their own workspaces and the templates assigned to them.

## Where roles live

The portal reads the `poddy-rbac` ConfigMap in the workspace namespace (same namespace as workspaces). The key is `rbac.yaml`:

```yaml
admins:
  - admin@example.com
  - group:platform

teams:
  backend:
    members:
      - alice@example.com
      - user:bob
      - group:backend-devs

templates:
  go:
    owners:
      - dev@example.com
    users:
      - alice@example.com
      - group:contractors
    teams:
      - backend
```

Change the ConfigMap name with `-rbac-configmap` / `PODDY_RBAC_CONFIGMAP`. A missing ConfigMap is valid: no admins, no teams, and all templates are public.

The portal writes this document through the UI/API. You can also edit it with `kubectl`; changes apply on the next request. The portal's Role needs `get/list/watch/create/update/patch` on `configmaps` (the Helm chart and `deploy/poddy.yaml` include it).

## Identity matching

Every entry is matched against the signed-in identity (username, email, groups):

| Entry | Matches |
|-------|---------|
| `alice@example.com` | username **or** email, case-insensitive |
| `user:alice` | username only |
| `email:alice@example.com` | email only |
| `group:backend` | one of the identity's groups |
| `*` | every authenticated user |

Use `group:` for IdP groups, or plain email/username for small setups. Team membership uses the same matching rules.

> `*` is powerful. `admins: ["*"]` makes everyone an admin; `owners: ["*"]` lets everyone manage a template. Use it only in `users` when you want a template with owners to remain public.

## Template access

A template with **no entry** under `templates` is **public**: every authenticated user can create workspaces from it.

Once an entry exists, only these identities can use the template:

- global admins;
- the template `owners`;
- identities in `users`;
- members of the teams in `teams`.

An entry with owners but no users/teams is restricted to the owners (and admins). To keep owners and also allow everyone, add `"*"` to `users`.

Access changes do **not** delete existing workspaces. They only affect who can create new ones.

### Who can edit access

| Action | Global admin | Template owner |
|--------|--------------|----------------|
| See the template | yes | yes |
| Create workspaces from it | yes | yes |
| Edit `users` / `teams` | yes | yes |
| Edit `owners` | yes | no (field read-only) |
| Manage teams | yes | no |
| List team names | yes | yes (for assignment) |

An owner who clears `users` and `teams` keeps the template restricted to its owners, because the `owners` list is preserved. Only an admin can remove all owners and access lists, which deletes the entry and makes the template public again.

The server rejects unknown team names when saving access.

## Workspace ownership

Each workspace gets an owner at creation:

1. the OIDC email, if present;
2. otherwise the username;
3. otherwise the subject.

The owner is stored in the pod annotation `poddy.io/owner` (not a label: emails are not valid label values).

- Global admins see and manage every workspace.
- Other users see only their own workspaces (list, delete, terminal, VS Code).
- Template owners do **not** automatically see other users' workspaces. Only global admins do.
- Workspaces created before this feature have no owner annotation. They remain visible to global admins only; non-admins will not see them, and only admin SSH keys can reach them.
- SSH access follows the same owner. Self-service keys added from **SSH keys** are bound to the signed-in identity server-side. Administrator-managed keys are matched by comment: `alice@example.com` can only reach workspaces owned by `alice@example.com`; for non-email identities use `user:<identity>` (for example `user:token` for token auth). Admin keys (`admin` or `*`) can reach every workspace. This check is enforced by the bastion on every connection, not only by the UI.
- In local editor mode, the portal marks the SSH keys it manages as admin keys. Cluster deployments run with `-editor=` and do not add local keys.

Trusted local mode (no token, no OIDC) acts as a global admin. A shared token has the single identity `token` and is not a global admin unless `auth.tokenAdmin=true`.

## UI

- **Users** (admins and owners): dedicated page listing known users and groups, with search. Admins also see which teams each identity belongs to; owners only see the directory.
- **Teams** (admins): dedicated page to create, edit members, and delete teams. Rename by creating a new team and moving its members. Deleting a team removes it from every template; if a template's access becomes empty (no owners/users/teams), the entry is deleted and the template becomes public.
- **Templates** (admins and owners): opens a full-page list of the templates you can manage. Search filters by template name, description, image, or version; select a template to edit access.
- **Template access**: owners, users, and team members are displayed as table rows with **Add** / **Remove** buttons. The add field autocompletes known users and groups from `GET /api/users`; free-form emails, `user:` and `group:` entries are still accepted.
- Workspace cards show the owner badge to global admins.
- The topbar shows the current role (`admin`, `owner`, or nothing for a regular user).

## API

| Method & path | Who | Purpose |
|---------------|-----|---------|
| `GET /api/me` | authenticated | username, email, groups, method, `isAdmin`, `ownedTemplates` |
| `GET /api/templates` | authenticated | templates the user can use, with `canManage` / `owned` |
| `GET /api/templates/{name}/access` | admin or owner | current owners/users/teams |
| `PUT /api/templates/{name}/access` | admin or owner | body `{"owners":[],"users":[],"teams":[]}` |
| `GET /api/users` | admin or template owner | known users and groups for the Users page and autocomplete |
| `GET /api/teams` | admin or template owner | team names; members only for admins |
| `PUT /api/teams/{name}` | admin | body `{"members":[]}` |
| `DELETE /api/teams/{name}` | admin | delete and detach from templates |
| `GET /api/volumes` | authenticated | volumes, summary, and backup availability; filtered by ownership for non-admins |
| `DELETE /api/volumes/{name}` | admin | delete an unassigned volume |
| `GET/POST /api/volumes/{name}/backups` | owner or admin | list or create backups for a volume |
| `DELETE /api/volumes/{name}/backups` | owner or admin | delete a backup object |
| `POST /api/volumes/{name}/restore` | owner or admin | restore a backup into the source volume or, with `target`/`targetRegion`, another volume the caller can access |

`GET /api/workspaces` is filtered by ownership for non-admins. `POST /api/workspaces` checks template access, records the owner, and prefixes the workspace name with the authenticated username (for example, `alice-my-workspace`) so names are unique per user. The shared token gets a `token-` prefix; trusted local mode without authentication does not prefix. `DELETE /api/workspaces/{name}`, the terminal endpoint, and the local VS Code launch endpoint require ownership or admin.

`GET /api/volumes` follows the same ownership model: admins see every volume,
users see only volumes assigned to their own workspaces, and unassigned volumes
are admin-only. Volume deletion is admin-only and restricted to unassigned
volumes. Volume backup and restore endpoints are available to admins and to the
owner of an assigned volume. Live backup is allowed with a consistency warning;
restore requires the target workspace to be stopped. Copying a backup into
another volume requires access to both the source and target volumes. See
[Volumes and backups](volumes.md).

## Helm

```yaml
rbac:
  create: true
  configMapName: ""        # default "<fullname>-rbac"
  createConfigMap: true    # seed the ConfigMap on install
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

The chart adds the ConfigMap permissions to the portal Role. `helm upgrade` re-applies the seed values; if admins have edited access in the UI, either set `rbac.createConfigMap: false` after the initial install or keep the values in sync. The portal's writes are lost only if a later `helm upgrade` overwrites the ConfigMap.

## Local demo

See [oidc.md](oidc.md#local-demo-with-dex) and the `deploy/dex` directory. The script seeds:

- admins: `admin@example.com`, `ops@example.com`
- teams: `backend` (`alice@example.com`, `bob@example.com`), `frontend` (`carol@example.com`)
- template `go`: owner `dev@example.com`, direct user `dave@example.com`, no team yet

Then sign in as `dev`, assign `backend` to `go`, and sign in as `alice` or `bob` to see it. `carol` and `erin` see only `base`.

## Limits

- One workspace namespace per portal; one RBAC document per portal.
- No per-template quotas or workspace sharing between users. With PostgreSQL enabled, the portal records an audit trail for workspace, team, template-access, external-auth, terminal/VS Code, and SSH actions; see [PostgreSQL audit actions](postgres-schema.md#audit-actions).
- No database is required: back up the ConfigMap if you rely on UI-managed access, or enable the optional PostgreSQL metadata mirror.
- RBAC writes are serialized within a portal replica and retried on conflicts. With multiple replicas, avoid simultaneous edits.
