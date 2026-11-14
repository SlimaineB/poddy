# External auth for Git providers

poddy can connect a user's external OAuth2 account (GitHub, GitLab, Gitea, Azure DevOps, Bitbucket, or a generic OIDC provider) and inject a fresh access token into their workspace. This is for **workspace credentials**, not for portal login; portal login is [OIDC](oidc.md).

Typical use: a private Git repository inside a workspace needs a token. The portal refreshes the token server-side, so long-running workspaces keep working after the original access token expires.

## How it works

1. An admin configures one or more providers on the portal (environment variables, or Helm values).
2. A template declares which providers it needs under `externalAuth:`.
3. The user opens **Connections** in the portal and connects each provider.
4. When a workspace is created or started, the portal injects the access token into the workspace as environment variables.
5. The workspace can also ask the portal for a refreshed token or Git credentials through a short-lived workspace bearer token.

Tokens are stored AES-GCM encrypted in PostgreSQL `external_auth_tokens`; the old `poddy-external-auth` Secret is only an import source. Tokens are stripped from stopped StatefulSet templates.

## Configure providers

Providers use indexed environment variables:

```bash
PODDY_EXTERNAL_AUTH_0_ID=github
PODDY_EXTERNAL_AUTH_0_TYPE=github
PODDY_EXTERNAL_AUTH_0_DISPLAY_NAME=GitHub
PODDY_EXTERNAL_AUTH_0_CLIENT_ID=Iv1.abc123
PODDY_EXTERNAL_AUTH_0_CLIENT_SECRET=...
```

`TYPE` selects defaults for URLs, scopes, Git username and environment variables. `ID` is the stable identifier used by templates and must match `[A-Za-z0-9][A-Za-z0-9._-]{0,62}`. Indices may be sparse.

| Variable | Required | Description |
|----------|----------|-------------|
| `_ID` | yes | Provider ID used in templates. |
| `_TYPE` | no | `github`, `gitlab`, `gitea`, `azure-devops`, `azure-devops-entra`, `bitbucket-cloud`, or `oidc` (default). |
| `_DISPLAY_NAME` | no | Name shown in the UI. Defaults to the type. |
| `_DISPLAY_ICON` | no | Optional icon key for the UI. |
| `_CLIENT_ID` | yes | OAuth2 client ID. |
| `_CLIENT_SECRET` | yes* | OAuth2 client secret. *Use the Helm existing Secret for production. |
| `_AUTH_URL` | no | Authorization endpoint; defaults by type. |
| `_TOKEN_URL` | no | Token endpoint; defaults by type. |
| `_VALIDATE_URL` | no | Optional validation endpoint. |
| `_REVOKE_URL` | no | Optional revoke endpoint. |
| `_REDIRECT_URL` | no | Exact callback URL; defaults to `<portal>/external-auth/<id>/callback`. |
| `_SCOPES` | no | Comma/space-separated scopes; defaults by type. |
| `_REGEX` | no | Git host regex; defaults by type. |
| `_USERNAME` | no | Git username; defaults to `x-access-token` for GitHub and `oauth2` otherwise. |
| `_ENV` | no | Comma-separated environment variable names; defaults by type. |
| `_PKCE` | no | `true` (default) or `false`. |

Provider-wide defaults:

| Type | Scopes | Git username | Workspace env |
|------|--------|--------------|---------------|
| `github` | `repo`, `read:user`, `user:email` | `x-access-token` | `GITHUB_TOKEN`, `GH_TOKEN` |
| `gitlab` | `write_repository`, `read_api` | `oauth2` | `GITLAB_TOKEN` |
| `gitea` | `read:user`, `write:repository` | `oauth2` | `GITEA_TOKEN` |
| `azure-devops`, `azure-devops-entra` | provider default | `oauth2` | — |
| `bitbucket-cloud` | provider default | `oauth2` | — |
| `oidc` | provider default | `oauth2` | — |

Every provider also gets `PODDY_EXTERNAL_AUTH_<ID>_TOKEN`, where `<ID>` is uppercased and non-alphanumeric characters become `_`.

Register this callback URL with the provider:

```
https://<portal-host>/external-auth/<provider-id>/callback
```

If the portal is behind a proxy, set `PODDY_EXTERNAL_AUTH_REDIRECT_BASE` to the public base URL. The workspace needs to reach the portal too; set `PODDY_EXTERNAL_AUTH_URL` when the URL derived from the creation request is not reachable from the pod.

## Declare external auth in a template

Add `externalAuth` to `template.yaml`:

```yaml
name: go
displayName: Go
image: ghcr.io/slimaineb/poddy-workspace:latest
externalAuth:
  - id: github
    env: [GITHUB_TOKEN, GH_TOKEN]   # optional override
  - id: gitlab
    optional: true
```

| Field | Description |
|-------|-------------|
| `id` | Provider ID configured on the portal. |
| `optional` | If true, the workspace can start without a connection. |
| `env` | Override the environment variable names for this template. |

A required provider that is not connected blocks creation/start with HTTP `409` and a structured response:

```json
{
  "error": "connect external auth first: github",
  "missing": ["github"],
  "providers": [
    {"id": "github", "displayName": "GitHub", "authenticated": false, "loginUrl": "/external-auth/github/login"}
  ]
}
```

The UI shows the **External accounts** notice in the create dialog and disables **Create workspace** until required accounts are connected.

## What the workspace receives

For each required provider, the portal sets:

| Variable | Value |
|----------|-------|
| provider env names (`GITHUB_TOKEN`, ...) | Access token. |
| `PODDY_EXTERNAL_AUTH_<ID>_TOKEN` | Access token. |
| `PODDY_PORTAL_URL` | Portal base URL reachable from the workspace. |
| `PODDY_WORKSPACE_NAME` | Workspace name. |
| `PODDY_WORKSPACE_TOKEN` | Signed bearer token for the workspace. |
| `PODDY_EXTERNAL_AUTH_IDS` | Comma-separated required provider IDs. |
| `GIT_ASKPASS` | `/usr/local/bin/poddy-git-askpass`. |
| `GIT_TERMINAL_PROMPT` | `0`. |

The base workspace image includes:

```bash
poddy-external-auth token github          # prints a refreshed access token
poddy-external-auth token gitlab
```

Git uses the askpass helper automatically, so this works even after the original token expires:

```bash
git clone https://github.com/org/private-repo.git
```

The helper calls the portal, which refreshes the token if needed. The workspace bearer token is valid for 30 days and is scoped to one workspace/owner.

## Portal API

| Method & path | Auth | Purpose |
|---------------|------|---------|
| `GET /api/external-auth?ids=github,gitlab` | user session | Connection status. |
| `POST /api/external-auth/{id}/disconnect` | user session | Delete the stored token. |
| `GET /external-auth/{id}/login?next=/` | user session | Start OAuth2. |
| `GET /external-auth/{id}/callback` | OAuth2 state cookie | Finish OAuth2. |
| `GET /api/workspaces/{name}/external-auth/{id}/token` | `X-Poddy-Workspace-Token` | Refreshed token JSON. |
| `GET /api/workspaces/{name}/git-askpass?prompt=...` | `X-Poddy-Workspace-Token` | Git username or password. |

The workspace endpoints reject providers that are not declared by the workspace's template.

## Helm

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

Create the configuration Secret:

```bash
kubectl create secret generic poddy-external-auth-config \
  --from-literal=external-auth-key="$(openssl rand -hex 32)" \
  --from-literal=provider-github-client-secret=... \
  --from-literal=provider-gitlab-client-secret=... \
  -n poddy-system
```

Alternatively put `key` and `clientSecret` in values for a quick test; the chart creates `poddy-external-auth-config` for you. Do not commit real secrets.

Per-user tokens are stored in PostgreSQL `external_auth_tokens`. The old `externalAuth.tokenSecretName` Secret (default `<fullname>-external-auth`) is imported only when the table is empty and is no longer updated after that. The chart still grants Secret permissions for SSH keys and environment Secrets.

`externalAuth.key`/`existingSecret` is required when `replicaCount > 1`, otherwise each replica would encrypt with a different ephemeral key. The same stable key is also used for encrypted share links and environment secrets in PostgreSQL. When no key is set, the database URL is the fallback key.

## Quick manifest

In `deploy/poddy.yaml`, uncomment the external auth environment block and add a Secret with the key and client secrets:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: poddy-external-auth-config
  namespace: poddy
type: Opaque
stringData:
  external-auth-key: change-me
  provider-github-client-secret: change-me
```

```yaml
env:
  - name: PODDY_EXTERNAL_AUTH_KEY
    valueFrom:
      secretKeyRef:
        name: poddy-external-auth-config
        key: external-auth-key
  - name: PODDY_EXTERNAL_AUTH_0_ID
    value: github
  - name: PODDY_EXTERNAL_AUTH_0_TYPE
    value: github
  - name: PODDY_EXTERNAL_AUTH_0_CLIENT_ID
    value: Iv1.abc123
  - name: PODDY_EXTERNAL_AUTH_0_CLIENT_SECRET
    valueFrom:
      secretKeyRef:
        name: poddy-external-auth-config
        key: provider-github-client-secret
```

## Troubleshooting

| Symptom | Check |
|---------|-------|
| **Connections** is hidden | No providers configured, or the portal cannot read its environment. |
| `unknown external auth provider` | Template `externalAuth.id` does not match a configured `_ID`. |
| `409 connect external auth first` | Connect the provider in **Connections**; check optional providers. |
| Callback fails with `redirect_uri` mismatch | Register `<portal>/external-auth/<id>/callback` exactly; set `redirectBase`. |
| Workspace has no token | Provider is optional, or workspace was created before the connection. Restart it after connecting. |
| Git still prompts for a password | Check `GIT_ASKPASS` and `PODDY_WORKSPACE_TOKEN` in the pod; check the portal logs. |
| Tokens stop working after a portal restart | Set a stable `PODDY_EXTERNAL_AUTH_KEY` (or Helm `externalAuth.key`/`existingSecret`). |
| `decrypt token store` | The encryption key changed. Disconnect/reconnect providers or restore the previous key. |
| Git host matches multiple providers | Make `_REGEX` values disjoint. |

## Security notes

- Prefer `existingSecret`/`externalAuth.existingSecret`; avoid inline secrets in values.
- The token store Secret is encrypted, but anyone with `get secrets` in the namespace can read it. Restrict RBAC.
- The workspace bearer token is a bearer credential. Do not print it in shared logs.
- Use HTTPS for the portal and providers; cookies are `Secure` when TLS is detected.
- Tokens are per user and per provider; disconnecting deletes the stored token.
