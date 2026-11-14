# Okta SSO

This page is a complete Okta example for the generic [OIDC / SSO](oidc.md)
integration. It covers the Okta application, the `groups` claim, the Kubernetes
Secret, and the Helm values.

The example values live in
[`examples/okta/values.yaml`](https://github.com/SlimaineB/poddy/blob/main/examples/okta/values.yaml).

## 1. Create the Okta application

1. In the Okta Admin Console, go to **Applications** > **Applications**.
2. Choose **Create App Integration** > **OIDC - OpenID Connect** >
   **Web Application**.
3. Set the sign-in redirect URI to the portal callback:
   `https://poddy.example.com/oidc/callback`.
4. Set the sign-out redirect URI to `https://poddy.example.com/`.
5. Assign the users or groups that should be allowed to sign in.

Use a **confidential** client with a client secret. The portal also supports a
public client with PKCE, but a confidential client is the usual Okta setup for a
server-side web application.

## 2. Choose the issuer

Okta has two authorization server modes:

| Mode | Issuer | Groups claim |
|------|--------|--------------|
| Custom authorization server (recommended) | `https://<org>.okta.com/oauth2/default` | Add a claim to the authorization server. |
| Org authorization server | `https://<org>.okta.com` | Configure the groups claim on the application's **Sign On** tab. |

The issuer must exactly match the `iss` claim. Do not add a trailing slash.

### Custom authorization server

1. Go to **Security** > **API** > **Authorization Servers** and open the
   `default` server.
2. Open **Claims** > **Add Claim**.
3. Use these values:
   - **Name**: `groups`
   - **Include in token type**: `ID Token`
   - **Value type**: `Groups`
   - **Filter**: `Matches regex` with `.*`, or a narrower filter such as
     `Starts with` `poddy-`.
4. If your authorization server requires a scope for the claim, add a `groups`
   scope under **Scopes** and include it in `oidc.scopes`.

### Org authorization server

1. Open the Okta application and go to the **Sign On** tab.
2. Under **OpenID Connect ID Token**, set **Groups claim type** to `Filter`.
3. Set **Groups claim filter** to `groups` `Matches regex` `.*`, or a narrower
   filter such as `StartsWith` `poddy-`.
4. Save the application.

The portal reads the groups from the ID token. If the claim is missing, it also
merges claims from the provider's userinfo endpoint.

## 3. Create the Kubernetes Secret

Prefer an existing Secret over putting the client secret in values:

```bash
export OKTA_CLIENT_ID='0oa...'
export OKTA_CLIENT_SECRET='...'

kubectl -n poddy-system create secret generic poddy-oidc \
  --from-literal=oidc-client-id="$OKTA_CLIENT_ID" \
  --from-literal=oidc-client-secret="$OKTA_CLIENT_SECRET" \
  --from-literal=oidc-session-key="$(openssl rand -hex 32)"
```

The chart reads these keys by default:

- `oidc-client-id`
- `oidc-client-secret`
- `oidc-session-key`

The session key must be stable when running more than one portal replica.

## 4. Helm values

```yaml
oidc:
  enabled: true
  issuer: https://dev-123456.okta.com/oauth2/default
  existingSecret: poddy-oidc
  redirectUrl: https://poddy.example.com/oidc/callback
  scopes: "openid,profile,email"
  allowedGroups:
    - poddy-users
  usernameClaim: preferred_username
  emailClaim: email
  groupsClaim: groups
  sessionTtl: 12h
```

If your Okta authorization server defines a `groups` scope, use:

```yaml
  scopes: "openid,profile,email,groups"
```

`allowedGroups` and `allowedUsers` are case-insensitive. An empty list allows
any authenticated Okta user. These lists only gate sign-in; RBAC still controls
admins, template owners, teams, and workspace ownership. See
[RBAC and ownership](rbac.md).

## 5. Install or upgrade

```bash
helm upgrade --install poddy oci://ghcr.io/slimaineb/charts/poddy \
  -n poddy-system --create-namespace --version 0.1.7 \
  -f values.yaml
```

If the chart is private, run `helm registry login ghcr.io` first. From a source
checkout, replace the OCI reference with `deploy/helm/poddy`.

## 6. Verify

1. Open `https://poddy.example.com` and click **Continue with SSO**.
2. Sign in with an assigned Okta user.
3. Confirm the user appears in the portal and can create a workspace.
4. If you use group-based RBAC, confirm the group name in Okta matches the RBAC
   document exactly (matching is case-insensitive).
5. If the user is in `allowedGroups`, sign-in succeeds; otherwise the portal
   rejects the login.

Inspect the ID token with Okta's token preview or your browser's developer tools.
Do not paste production tokens into third-party sites.

## Troubleshooting

| Symptom | Check |
|---------|-------|
| `issuer mismatch` | Use the exact issuer from the discovery document: `https://<org>.okta.com` or `https://<org>.okta.com/oauth2/default`. Remove trailing slashes. |
| `redirect_uri mismatch` | The Okta app must contain exactly `https://poddy.example.com/oidc/callback`. Check the public host and scheme behind the ingress. |
| `invalid_scope` | Remove `groups` from `oidc.scopes` unless the authorization server defines that scope. |
| Login succeeds but no groups | Add the groups claim to the ID token, or use a narrower filter that still matches the user's groups. |
| SSO works but a template is missing | Check template access, team membership, and `admins` in the [RBAC document](rbac.md). |
| Sessions drop after restart | Set a stable `oidc-session-key` in the Secret. |
| Login loops back to the login page | The browser must use the same public host as `redirectUrl`; check TLS, cookies, and proxy headers. |

## Related

- [OIDC / SSO](oidc.md) — generic provider configuration and flags.
- [RBAC and ownership](rbac.md) — admins, teams, and template access.
- [Production deployment](deploy.md) — secrets, ingress, and scaling.
