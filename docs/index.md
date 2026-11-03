# poddy

Kubernetes development workspaces with a browser UI, an SSH bastion, and VS Code Remote-SSH.

## Documentation

- [Deployment](deploy.md) — production Helm deployment, secrets, ingress, and HA.
- [Clean k3s test deployment](k3s-test.md) — end-to-end local k3s setup.
- [OIDC](oidc.md) — SSO login and the local Dex demo.
- [RBAC and ownership](rbac.md) — admins, teams, template access, and SSH ownership.
- [Workspace templates](templates.md) — template schema, pod contract, images, and versioning.
- [External authentication](external-auth.md) — GitHub/GitLab tokens inside workspaces.
- [Workspace links](links.md) — extra actions on workspace cards.

The portal UI itself is embedded in the Go binary and served by the poddy deployment. This Pages site only documents it.
