# Okta OIDC example

`values.yaml` contains the Helm values for an Okta OIDC application:

- custom authorization server issuer (`/oauth2/default`);
- `groups` claim from the ID token;
- an existing Kubernetes Secret for the client ID, client secret, and session
  key;
- an optional `allowedGroups` sign-in allowlist.

See [`docs/okta.md`](../../docs/okta.md) for the full walkthrough: Okta
application setup, groups claim, Secret creation, verification, and
troubleshooting.
