# poddy

**Self-service Kubernetes development workspaces for teams that want to ship, not manage infrastructure.**

poddy gives every developer a persistent Linux workspace on Kubernetes. They open it from the browser or connect their favourite IDE over SSH. Platform teams keep SSO, ownership, quotas, images, networking, volumes, and backups under control. Users never need `kubectl` or a kubeconfig.

**Documentation:** <https://slimaineb.github.io/poddy/>

![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)

## Why poddy

- **Users get a workspace, not a Kubernetes tutorial.** Create a workspace from the portal, open a terminal, or connect VS Code, Cursor, PyCharm, or JupyterLab.
- **Workspaces survive laptop reboots.** The home directory is a persistent volume. Stop a workspace when it is idle and start it later with the same files, tools, and configuration.
- **The IDE you already use stays the IDE you use.** One click writes the SSH configuration and opens Remote-SSH or JetBrains Gateway. Browser terminal and browser VS Code are built in.
- **Platform teams stay in control.** OIDC/SSO, owners, teams, template access, quotas, audit events, private registries, and optional S3 volume backups are part of the product.
- **Bring your own images and templates.** Templates are plain YAML: choose an image, expose parameters, add sidecars, ports, and links. No proprietary container format.
- **Runs on the Kubernetes you already have.** k3s, EKS, GKE, AKS, OpenShift, or any conforming cluster. MIT licensed, no SaaS control plane required.
- **Small enough to understand, serious enough for production.** Two Deployments, PostgreSQL for the control plane, Kubernetes for the runtime, and a clear security boundary for every workspace.

## What you get

| Capability | What it means |
|------------|---------------|
| **Web portal** | Create, stop, start, restart, inspect, and delete workspaces. Search, pagination, topology, and a world map for multi-region deployments. |
| **Real terminals and IDEs** | Browser terminal, VS Code Web, VS Code/Cursor Remote-SSH, JetBrains Gateway, JupyterLab, and manual SSH. |
| **Persistent homes** | PVC per workspace, ephemeral storage when you do not need persistence, per-user dotfiles, and backup/restore to S3-compatible storage. |
| **Templates** | Reusable workspace definitions with images, CPU/memory/storage parameters, sidecars, environment variables, links, and external-auth requirements. |
| **Team access** | OIDC/SSO or a shared token, global admins, read-only auditors, template owners, teams, per-template access, workspace ownership, and quotas. |
| **External auth** | Connect GitHub, GitLab, Gitea, Azure DevOps, Bitbucket, or generic OAuth2 accounts. Refreshed tokens are injected into workspaces and used by Git automatically. |
| **Secrets** | Encrypted environment variables and secrets at template, team, user, and workspace scope. |
| **Multi-region** | One central hub and isolated region managers connected by outbound tunnels. No VPN peering or hub-to-region inbound access. |
| **Observability** | Live CPU/memory, Prometheus history, admin monitoring overview, structured logs, and a PostgreSQL-backed audit trail. |
| **Kubernetes-native operations** | Helm charts, least-privilege ServiceAccounts, PDBs, topology spread, registry mirrors, and no cluster credentials for users. |

## Works with your workflow

| Way to work | Requirement |
|-------------|-------------|
| **Browser terminal** | Nothing beyond the portal. |
| **VS Code Web** | Enabled by the template (`browserVSCode: "true"`). |
| **VS Code / Cursor Remote-SSH** | Install the Poddy extension once per machine. The portal handles keys and SSH config. |
| **PyCharm / JetBrains Gateway** | Enable **Parse config file ~/.ssh/config** in Gateway. The SSH dialog provides the config. |
| **JupyterLab** | Workspace template with a JupyterLab sidecar on port `8888`. |
| **SSH / CLI** | The portal generates a `ProxyCommand` config, or use `poddy login`, `poddy ssh`, `poddy cp`, and `poddy port-forward`. |
| **Ports and previews** | Open any listening TCP port through the authenticated portal proxy, or share it internally or publicly. |

## Quick start

This path uses the published GHCR images and Helm. You need:

- Kubernetes 1.28+ or k3s
- PostgreSQL 13+ (the example below installs a development instance)
- Helm 3.13+, `kubectl`, `openssl`, and `ssh-keygen`

### 1. Create the namespace and portal token

```bash
kubectl create namespace poddy-system --dry-run=client -o yaml | kubectl apply -f -

TOKEN="$(openssl rand -hex 32)"
kubectl -n poddy-system create secret generic poddy-auth \
  --from-literal=token="$TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -
```

### 2. Create a development PostgreSQL database

Skip this step if you already have PostgreSQL. Create the `poddy-database` Secret with your own connection string instead.

```bash
PGPASSWORD="$(openssl rand -hex 16)"
helm upgrade --install poddy-postgres oci://registry-1.docker.io/bitnamicharts/postgresql \
  --namespace poddy-system \
  --set auth.username=poddy \
  --set auth.password="$PGPASSWORD" \
  --set auth.database=poddy

kubectl -n poddy-system create secret generic poddy-database \
  --from-literal=database-url="postgres://poddy:${PGPASSWORD}@poddy-postgres-postgresql:5432/poddy?sslmode=disable" \
  --dry-run=client -o yaml | kubectl apply -f -
```

### 3. Install the portal

```bash
helm upgrade --install poddy oci://ghcr.io/slimaineb/charts/poddy \
  --namespace poddy-system \
  --version 0.1.10 \
  --set image.repository=ghcr.io/slimaineb/poddy \
  --set image.tag=latest \
  --set auth.existingSecret=poddy-auth \
  --set database.existingSecret=poddy-database \
  --set ssh.service.type=ClusterIP \
  --set ssh.publicHost=127.0.0.1 \
  --set ssh.publicPort=2222 \
  --set workspaceNamespace=poddy-workspaces \
  --set createWorkspaceNamespace=true \
  --wait --timeout 10m
```

### 4. Install the default workspace templates

Templates are a separate release so they can be versioned and rolled back independently.

```bash
helm upgrade --install poddy-templates oci://ghcr.io/slimaineb/charts/poddy-templates \
  --namespace poddy-workspaces \
  --version 0.1.10
```

### 5. Open the portal

```bash
kubectl -n poddy-system port-forward svc/poddy 8080:80
```

Read the token:

```bash
kubectl -n poddy-system get secret poddy-auth \
  -o jsonpath='{.data.token}' | base64 -d
```

Open <http://127.0.0.1:8080>, sign in, click **New workspace**, and choose **Base**, **Go**, **Node.js**, or **Python**.

For desktop SSH, start a second port-forward:

```bash
kubectl -n poddy-system port-forward svc/poddy-ssh 2222:2222
```

That is enough to create a workspace, open the browser terminal, open VS Code Web, add an SSH key, and connect your local IDE.

## Architecture

```text
Browser ──────────► Portal (:8080) ──────────► Kubernetes API
                                               (workspace lifecycle)

VS Code / Cursor ──► Headscale mesh ─────────► workspace agent ──► sshd (:22)
                 └─► Portal WSS ──► port-forward / region tunnel ─► sshd (:22)

ssh / PyCharm ─────► Portal (:2222) ─────────► workspace agent ──► sshd (:22)
```

Desktop SSH has two transports. With `mesh.enabled=true`, VS Code and Cursor use the bundled helper to join a Headscale/Tailscale tailnet and dial the workspace over WireGuard. Without mesh, they open an authenticated WebSocket to the portal, which reaches the workspace through the Kubernetes port-forward API or the regional tunnel. Manual SSH and JetBrains Gateway use the portal SSH listener; `poddy ssh` uses the mesh when it is enabled. The portal selects the transport and the extension follows it, so users do not choose a mode.

The portal serves the UI/API, manages workspaces, and terminates SSH in the same Deployment without exposing workspace pods. Workspace agents connect outbound to the portal; regions use their outbound tunnel. PostgreSQL stores the control plane: users, RBAC, quotas, access grants, encrypted tokens/secrets, share links, and audit events. Kubernetes remains the source of truth for workspaces, pods, and PVCs.

In multi-region mode, a central hub owns authentication and the UI while each region manager owns its Kubernetes cluster. Region managers connect outbound to the hub, so the hub does not need a private network path to every cluster.

See [Architecture](https://slimaineb.github.io/poddy/architecture/) for request flows, HA state, and component boundaries.

## Production checklist

Before exposing poddy to a team:

- [ ] Enable OIDC/SSO and set `auth.tokenAdmin: false`.
- [ ] Terminate TLS at an ingress or load balancer and use HTTPS for the portal.
- [ ] Use a managed PostgreSQL 13+ instance with backups and TLS.
- [ ] Set stable Secrets for `oidc.sessionKey`, `externalAuth.key`, the SSH CA, and the SSH host key.
- [ ] Pin a released image tag and Helm chart version; do not run `latest` in production.
- [ ] Create the workspace namespace yourself and set `createWorkspaceNamespace: false`.
- [ ] Protect template ConfigMaps: whoever can write a template can control the pod spec.
- [ ] Restrict the metrics Service to your monitoring stack.
- [ ] Configure volume backups or storage snapshots for persistent homes.
- [ ] Read the [operations guide](https://slimaineb.github.io/poddy/operations/) and [security model](https://slimaineb.github.io/poddy/rbac/).

## Documentation map

| I want to… | Read |
|------------|------|
| Try poddy quickly | [Quick start](https://slimaineb.github.io/poddy/getting-started/) |
| Understand the product and concepts | [Product tour](https://slimaineb.github.io/poddy/guided-tour/) |
| Run the full demo stack with Gitea, Dex, MinIO, and PostgreSQL | [Full demo](https://slimaineb.github.io/poddy/demo/) |
| Use workspaces day to day | [Workspaces](https://slimaineb.github.io/poddy/workspaces/) |
| Install for production | [Kubernetes deployment](https://slimaineb.github.io/poddy/deploy/) |
| Operate, upgrade, back up, and monitor | [Operations](https://slimaineb.github.io/poddy/operations/) |
| Run on Amazon EKS | [EKS guide](https://slimaineb.github.io/poddy/eks/) |
| Add my own image or template | [Templates](https://slimaineb.github.io/poddy/templates/) |
| Connect GitHub/GitLab inside a workspace | [External authentication](https://slimaineb.github.io/poddy/external-auth/) |
| Configure SSO | [OIDC](https://slimaineb.github.io/poddy/oidc/) / [Okta](https://slimaineb.github.io/poddy/okta/) |
| Control who can do what | [RBAC](https://slimaineb.github.io/poddy/rbac/) |
| Connect over SSH or an IDE | [SSH access](https://slimaineb.github.io/poddy/ssh/) |
| Understand the data model | [Data model](https://slimaineb.github.io/poddy/data-model/) / [PostgreSQL](https://slimaineb.github.io/poddy/postgres-schema/) |
| Fix a problem | [Troubleshooting](https://slimaineb.github.io/poddy/troubleshooting/) |

The documentation site lives in `website/` and is built with Astro and Starlight:

```bash
cd website
npm install
npm run dev
```

## Development

Contributor setup, local builds, test commands, and image publishing are in [Development](https://slimaineb.github.io/poddy/development/).

```bash
go test ./...
make image
```

The portal and workspace agent use the same Go module. Workspace images build from the reusable `ghcr.io/slimaineb/poddy-workspace-base` image.

## Security

- Users never receive cluster credentials. The portal talks to the Kubernetes API with a namespace-scoped Role.
- Workspace SSH access is owner-scoped. Self-service keys are bound to the signed-in identity; admin-managed keys are mapped by comment.
- The portal runs as non-root with a read-only root filesystem and dropped capabilities.
- External-auth tokens, share links, and environment secrets are encrypted with a stable key in PostgreSQL.
- Shared tokens are for local use and automation. Use OIDC for teams, and leave `tokenAdmin` disabled unless the token is a deliberate operator credential.
- Templates are cluster-privileged objects. Use Kubernetes RBAC and admission policies to protect the template write path.
- CI runs govulncheck, gosec, CodeQL, gitleaks, Trivy, kube-linter, Hadolint, npm audit, dependency review, and OpenSSF Scorecard. Dependabot keeps Go, npm, Docker, and GitHub Actions dependencies current.

## License

MIT
