# Okta OIDC example

`values.yaml` contains the Helm values for an Okta OIDC application:

- custom authorization server issuer (`/oauth2/default`);
- `groups` claim from the ID token;
- an existing Kubernetes Secret for the client ID, client secret, and session
  key;
- a required PostgreSQL connection Secret for the control plane;
- an optional `allowedGroups` sign-in allowlist.

See [the Okta guide](https://slimaineb.github.io/poddy/okta/) for the full walkthrough: Okta
application setup, groups claim, Secret creation, verification, and
troubleshooting.
