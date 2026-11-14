# Environment variables and secrets

poddy resolves environment variables from four scopes and injects the result
into the workspace's `workspace` container:

```text
workspace > user > team > template
```

The **Environment** page in the top bar manages values per scope. Changes are
resolved when a workspace is created or started, so restart a running workspace
to pick up new values.

## Scopes

| Scope | Target | Who can manage it |
|-------|--------|-------------------|
| Template | Template name | Global admins and template owners |
| Team | RBAC team name | Global admins |
| User | Email or username (case-insensitive) | The user, or global admins |
| Workspace | Workspace name | Workspace owner, or global admins |

Template values apply to every workspace using that template. Team values apply
to members of that team. When several teams define the same variable, teams are
applied in alphabetical order and the last one wins. User values override team
and template values; workspace values override everything.

## Names and values

- Names must match `[A-Za-z_][A-Za-z0-9_]*` and are limited to 255 characters.
- Names starting with `PODDY_` or `KUBERNETES_` are reserved, as are internal
  names such as `GIT_ASKPASS` and `GIT_TERMINAL_PROMPT`.
- Values may be empty and are limited to 64 KiB.
- Mark a value as a **secret** to encrypt it and inject it through Kubernetes.

Secrets are AES-GCM encrypted in PostgreSQL `env_vars`. The API never returns a
secret value; it returns `secret: true` and `set: true` so the UI can show that a
value exists. To replace a secret, enter a new value.

## Runtime injection

At create/start the portal resolves all scopes for the workspace owner and
template, then writes:

- plain values directly on the `workspace` container;
- secret values to the `pd-env-<workspace>` Secret and an `envFrom` reference.

The internal ordering means SSH keys, external-auth tokens, and the workspace
agent token override a variable with the same name. Plain variable names are
recorded in the `poddy.io/environment-env` annotation so they can be removed when
the workspace stops. Secret values are not written into the StatefulSet. On
delete, the portal removes the workspace Secret and PostgreSQL rows.

## Encryption key

Environment secrets use the same stable key chain as other encrypted control
plane data:

1. `PODDY_EXTERNAL_AUTH_KEY`
2. `PODDY_OIDC_SESSION_KEY`
3. `PODDY_DESKTOP_SECRET`
4. `PODDY_DATABASE_URL` as the final fallback

Set one of the explicit keys in production. If the fallback database URL
changes, existing encrypted secrets, tokens, and share links become unreadable.

## API

```text
GET    /api/environment?scope=<scope>&target=<target>
PUT    /api/environment
DELETE /api/environment?scope=<scope>&target=<target>&name=<name>
```

`PUT` accepts:

```json
{
  "scope": "workspace",
  "target": "my-workspace",
  "name": "API_TOKEN",
  "value": "secret-value",
  "secret": true
}
```

Region managers resolve environment through the hub
(`POST /api/internal/region/environment`); they do not need database
credentials.

## Limitations

- Changes apply on the next create/start, not to a running pod.
- Deleting a team removes its environment rows. Deleting a workspace removes its
  rows and Secret.
- Back up PostgreSQL; workspace PVC contents are separate.
