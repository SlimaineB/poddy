# OIDC / SSO

poddy supports the OpenID Connect authorization code flow with PKCE. OIDC is optional and can be combined with the shared access token. When OIDC is enabled, the login page shows **Continue with SSO**.

- Sessions are stateless, HMAC-signed cookies. No database is required.
- The shared token remains valid for API automation and acts as a global admin.
- OIDC identities are used by the RBAC model: global admins, template owners, teams, and per-template users. See [rbac.md](rbac.md).

## Local demo with Dex

A self-contained Dex setup lives in `deploy/dex`. It creates eight users with groups and a demo RBAC document. It is meant for local testing only.

### Quick start

Requirements: Docker, kubectl, Go, a reachable Kubernetes context, and a `poddy` namespace.

```bash
# If the namespace does not exist yet:
kubectl --context local create namespace poddy

# From the repository root:
./scripts/dev-oidc.sh
```

The script:

1. starts Dex with Docker Compose on `http://127.0.0.1:5556/dex`;
2. waits for the OIDC discovery document;
3. applies the demo RBAC ConfigMap (`deploy/dex/rbac.yaml`);
4. builds and starts the portal with OIDC enabled.

If your k3s context is not named `local`, set `KUBE_CONTEXT`. If the workspace namespace is not `poddy`, set `PODDY_NAMESPACE` (the demo RBAC file is rewritten to that namespace).

### Demo users

| User | Password | Role |
|------|----------|------|
| `admin@example.com` | `admin` | global admin |
| `ops@example.com` | `ops` | global admin |
| `dev@example.com` | `poddy` | owner of the `go` template |
| `alice@example.com` | `alice` | member of the `backend` team |
| `bob@example.com` | `bob` | member of the `backend` team |
| `carol@example.com` | `carol` | member of the `frontend` team |
| `dave@example.com` | `dave` | direct user of the `go` template |
| `erin@example.com` | `erin` | regular user (public `base` only) |

### Demo scenario

1. Open `http://127.0.0.1:8090` and sign in as `dev@example.com` / `poddy`.
2. Open **Templates** → search `Go` → open the `go` template.
3. In the template access page, tick the **backend** team, then save.
4. Sign out and sign in as `alice@example.com` or `bob@example.com`. The `go` template is now available; `base` remains public.
5. `dave@example.com` already has direct access to `go`; `carol@example.com` (frontend) and `erin@example.com` see only `base`.
6. Sign in as `admin@example.com` or `ops@example.com` to create teams or see every workspace.

In local editor mode the portal authorizes the SSH key it manages as an admin
key, so the **SSH** and **VS Code** buttons work for workspaces created by any
demo user.

### Stop

Stop the portal with `Ctrl+C`, then stop Dex:

```bash
docker compose -f deploy/dex/docker-compose.yaml down
```

The demo uses plain HTTP on `127.0.0.1` and known passwords. Never expose it.

## Provider configuration

- **Redirect URI**: `https://<poddy-host>/oidc/callback`
- **Scopes**: `openid profile email groups` (`groups` is optional but needed for group-based RBAC)
- **Client type**: confidential (with a client secret) or public (PKCE, no secret)
- **Issuer**: the exact issuer URL; it must match the `iss` claim.

## Helm values

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

## Flags and environment

| Flag | Environment | Description |
|------|-------------|-------------|
| `-oidc-issuer` | `PODDY_OIDC_ISSUER` | Issuer URL (enables OIDC). |
| `-oidc-client-id` | `PODDY_OIDC_CLIENT_ID` | Client ID. |
| `-oidc-client-secret` | `PODDY_OIDC_CLIENT_SECRET` | Client secret (empty = public client with PKCE). |
| `-oidc-redirect-url` | `PODDY_OIDC_REDIRECT_URL` | Callback URL (derived from the request when empty). |
| `-oidc-scopes` | `PODDY_OIDC_SCOPES` | Comma-separated scopes. |
| `-oidc-allowed-groups` | `PODDY_OIDC_ALLOWED_GROUPS` | Comma-separated group allowlist. |
| `-oidc-allowed-users` | `PODDY_OIDC_ALLOWED_USERS` | Comma-separated username/email allowlist. |
| `-oidc-username-claim` | `PODDY_OIDC_USERNAME_CLAIM` | Username claim (default `preferred_username`). |
| `-oidc-email-claim` | `PODDY_OIDC_EMAIL_CLAIM` | Email claim (default `email`). |
| `-oidc-groups-claim` | `PODDY_OIDC_GROUPS_CLAIM` | Groups claim (default `groups`). |
| `-oidc-session-key` | `PODDY_OIDC_SESSION_KEY` | Secret used to sign sessions. |
| `-oidc-session-ttl` | `PODDY_OIDC_SESSION_TTL` | Session lifetime (default `12h`). |
| `-rbac-configmap` | `PODDY_RBAC_CONFIGMAP` | RBAC ConfigMap name (default `poddy-rbac`). |

## Claims and groups

The portal reads the username, email and groups from the ID token. If a claim is absent, it merges claims from the provider's userinfo endpoint.

- **Keycloak**: add a `groups` mapper to the client scope (ID token + userinfo).
- **Microsoft Entra ID**: enable the `groups` claim in the token configuration, or map app roles.
- **Auth0**: add a custom claim with an Action.
- **Google**: no groups claim; use `allowedUsers` or a custom claim.

`allowedGroups` / `allowedUsers` are case-insensitive. Empty lists allow any authenticated user. These allowlists gate **sign-in**; RBAC roles (admins, owners, teams, template users) gate **what a signed-in user can do**.

## Sessions and HA

- Sessions are signed with `PODDY_OIDC_SESSION_KEY`. If unset, a random key is generated at startup (sessions are lost on restart and not shared across replicas).
- For `replicaCount > 1`, set a stable 32-byte key (or `oidc.existingSecret`). The chart fails if neither is set.
- Default session lifetime is 12h; sign-out clears the cookie.
- Token auth remains available when `auth.token` is set. To require SSO only, leave `auth.token` empty and do not set `PODDY_TOKEN`.
- RBAC reads and writes the same ConfigMap from every replica. Writes are serialized within a replica and retried on conflicts; avoid simultaneous RBAC edits from multiple replicas.

## Callback behind a proxy

Set `oidc.redirectUrl` (or `PODDY_OIDC_REDIRECT_URL`) to the public URL. If empty, the portal derives it from the request and honors `X-Forwarded-Proto` / `X-Forwarded-Host` set by ingress controllers.

## Troubleshooting

| Symptom | Check |
|---------|-------|
| `issuer mismatch` | The discovery document's issuer must equal `oidc.issuer` exactly (scheme, host, path, no trailing slash). |
| `redirect_uri mismatch` | The provider must allow exactly `https://<host>/oidc/callback`; check the public URL behind the proxy. |
| Login loops back to the login page | Cookie not set: check `Secure`/HTTPS, `SameSite`, and that the browser uses the same host as the redirect URL. |
| `401` after callback | Wrong client secret, clock skew, or the ID token audience/issuer does not match. |
| No groups | Add the groups claim/mapper; inspect the ID token and the userinfo response. |
| SSO works but a template is missing | Check RBAC: template access, team membership, or `admins` in the RBAC ConfigMap. |
| Sessions invalid after restart | Set a stable `PODDY_OIDC_SESSION_KEY`. |
| `127.0.0.1` works on Windows but not in the browser | WSL localhost forwarding may be disabled; use the WSL IP and update the issuer/redirect URLs consistently. |

## Security

- The local Dex demo uses HTTP and known passwords; it is not for production.
- Use HTTPS for the portal and the provider; the session cookie is `Secure` when TLS is detected.
- Prefer `oidc.existingSecret` over inline secrets.
- Set a stable random session key; rotate it to invalidate all sessions.
- Keep the shared token only for automation; it is a global admin.
