# poddy

Browser-accessible Kubernetes development workspaces with an SSH bastion. Open VS Code on Windows/WSL connected to a pod via SSH — no kubeconfig required for users.

**Documentation:** <https://slimaineb.github.io/poddy/>

## Features

- **Web portal**: Create, stop, start, restart, delete, and list workspaces; workspace details with startup logs and admin Pod/PVC manifests; browser terminal (`xterm.js`); a searchable, paginated topology view grouping users, their workspaces, and attached PVC/PV storage; a world map view plotting each region with workspace/user counts and pan/zoom.
- **Authentication**: OIDC / SSO (authorization code + PKCE) with group/user allowlists, or a shared access token for simple setups and API automation.
- **RBAC**: global admins, template owners, teams, and per-template user/team access. Workspaces are owned by their creator; admins see everything, users see their own. Users, teams, and templates each have a dedicated page; data is stored in a ConfigMap.
- **Extensible templates**: YAML templates are partial pod overlays with parameters and images; poddy merges them over sensible defaults. Built-in `base`, `go`, `node`, and `python` templates; add your own via `-templates-dir`, the separate `poddy-templates` Helm chart, or labeled ConfigMaps. The portal hot-reloads templates without restarting, and keeps multiple versions for one-label rollback.
- **External auth**: connect GitHub/GitLab/Gitea/Azure DevOps/Bitbucket OAuth2 accounts and inject refreshed tokens into workspaces. Templates declare required providers; the portal handles refresh, Git askpass, and per-user connections.
- **SSH bastion**: A separate `poddy-bastion` Deployment provides a single SSH entry point (`poddy-ssh-*` via ProxyJump) and port-forwards to workspace pods. Users add their public key from the **SSH keys** page; keys are bound to their signed-in identity and workspace owner.
- **VS Code launch**: Click **VS Code** to open Remote-SSH in a new window. In cluster mode the portal returns a short-lived signed ticket; the Poddy VS Code extension (installed once) enrolls the machine key, writes the SSH config, and opens `vscode://` without manual setup. In local editor mode the portal launches Windows VS Code directly. The **SSH** dialog remains available as a manual fallback, and **VS Code Web** opens the browser editor.
- **Persistent volumes**: PVC per workspace; ephemeral storage when empty. Stop keeps the volume; stopped specs are remembered in a ConfigMap. The **Volumes** page lists assigned and orphaned PVCs across regions, supports admin cleanup, backs up running or stopped volumes to S3-compatible storage, and restores a backup into the same or another volume. See [Volumes and backups](docs/volumes.md).
- **RBAC-scoped**: Portal and bastion run as separate non-root ServiceAccounts. The portal can manage workspaces; the bastion can only read pods and create port-forwards.
- **Multi-region**: One central hub provides authentication, RBAC, users, templates, the workspace list, and the UI. Each region runs its own manager and SSH bastion; the hub routes workspace operations, terminal, VS Code, logs, external-auth callbacks, and SSH-key sync to the correct region using signed identities. The top bar region filter applies to workspaces, topology, and the map. See [Multi-region](deploy.md#multi-region).
- **Helm charts**: The portal chart runs portal and bastion as separate Deployments; the templates chart is a separate release. The portal chart supports TLS ingress, LoadBalancer SSH, a shared host key, PDBs, resource limits, security context, central-hub regions, and region-manager mode.
- **Prometheus metrics**: A dedicated metrics listener exposes HTTP, workspace lifecycle, authentication, template reload, and workspace phase metrics, plus Go/process metrics. The Helm chart creates a ClusterIP metrics Service and can render a ServiceMonitor.
- **No database required**: Kubernetes is the source of truth; templates and RBAC are ConfigMaps. An optional PostgreSQL mirror adds metadata durability, an append-only audit log, and disaster-recovery snapshots. It is enabled with `database.url` / `PODDY_DATABASE_URL` and is never used for workspace or RBAC decisions; only the admin audit screen reads it.

## Architecture

```
Browser ──────────► Portal (:8080) ──────────► Kubernetes API (workspace lifecycle)
     │                     ▲
     │                     │
VS Code / ssh ────► Bastion (:2222) ─────────► Kubernetes API (pods/portforward)
                           │
                           └── ProxyJump / port-forward ──► Workspace pod :22
```

The portal and bastion are separate Deployments built from the same image. Scale
the portal for HTTP/API load and the bastion for SSH connections. In local mode,
`poddy` starts both processes together; Helm runs them independently.

In multi-region mode, a central hub runs the portal UI and routes workspace
requests to one region manager per Kubernetes cluster. Each region manager runs
the same image in `-region-mode`, trusts only signed hub identities, and keeps
its own workspace state, SSH bastion, and external-auth-free runtime. The hub
remains the source of truth for users, RBAC, templates, and external-auth tokens.

## Quick start (Helm + published images)

No Go or Docker build is required. The portal, bastion, Helm charts, and built-in
workspace images are published to GHCR.

### Prerequisites
- Kubernetes or k3s (running)
- `kubectl` and Helm 3.13+
- `ssh-keygen` and `openssl`
- Optional: VS Code with the **Remote-SSH** extension

### 1. Create the namespace and secrets
```bash
kubectl create namespace poddy-system --dry-run=client -o yaml | kubectl apply -f -

TOKEN="$(openssl rand -hex 32)"
kubectl -n poddy-system create secret generic poddy-auth \
  --from-literal=token="$TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -

ssh-keygen -t ed25519 -N '' -C 'user:token' -f ./poddy_key
ssh-keygen -t ed25519 -N '' -f ./poddy_host_ed25519
kubectl -n poddy-system create secret generic poddy-host-key \
  --from-file=ssh_host_ed25519_key=./poddy_host_ed25519 \
  --dry-run=client -o yaml | kubectl apply -f -
```

### 2. Install poddy
The portal chart is published to GHCR as an OCI artifact. If the package is
private, run `helm registry login ghcr.io` first.

```bash
helm upgrade --install poddy oci://ghcr.io/slimaineb/charts/poddy \
  --version 0.1.0 \
  --namespace poddy-system \
  --set image.repository=ghcr.io/slimaineb/poddy \
  --set image.tag=latest \
  --set auth.existingSecret=poddy-auth \
  --set ssh.service.type=ClusterIP \
  --set ssh.publicHost=127.0.0.1 \
  --set ssh.publicPort=2222 \
  --set-string ssh.authorizedKeys[0]="$(cat ./poddy_key.pub)" \
  --set ssh.existingHostKeySecret=poddy-host-key \
  --set workspaceNamespace=poddy-workspaces \
  --set createWorkspaceNamespace=true
```

If the GHCR packages are private, make them public for this quick start. The
chart's `imagePullSecrets` only covers the portal and bastion; workspace
templates need their own `imagePullSecrets` in the pod spec.

### 3. Start the port-forwards
Keep each command running in its own terminal:
```bash
kubectl -n poddy-system port-forward svc/poddy 8080:80
```
```bash
kubectl -n poddy-system port-forward svc/poddy-ssh 2222:2222
```

### 4. Open the UI
Read the token:
```bash
kubectl -n poddy-system get secret poddy-auth -o jsonpath='{.data.token}' | base64 -d
```
Open `http://127.0.0.1:8080` and enter the token.

### 5. Create a workspace
Click **New workspace**, pick **Base**, **Go**, **Node.js**, or **Python**, set parameters, **Create**. Wait for **Running**.

### 6. Connect
- **Terminal**: Click the terminal icon for a browser shell in the workspace user home (`/home/dev`).
- **SSH keys**: The **VS Code** button enrolls a dedicated `~/.ssh/poddy_<workspace>_ed25519` key automatically through the Poddy extension. You can also open **SSH keys** in the top bar and add any public key manually; keys are bound to your account and work for running workspaces (allow up to a minute for the Secret to reach the pod).
- **SSH**: Click **SSH** to copy a ready-to-use SSH config (ProxyJump through the bastion).
- **VS Code**: Click **VS Code** to open Remote-SSH on `/home/dev` in a new window. The first click on a machine requires installing the Poddy extension once (VSIX in `vscode-extension/`, or publish it to your marketplace); afterwards the flow is one click and needs no SSH setup.
- **VS Code Web**: Click **VS Code Web** to open the browser editor on `/home/dev` in a new tab when browser VS Code is enabled (`browserVSCode`, default `true` for the built-in templates).
- **Ports**: Click **Ports** on a running workspace to open a dedicated ports window, see its listening TCP ports, and open an app in a new tab. The same list is available in workspace details. The portal authenticates the request, checks workspace ownership, port-forwards to the pod (through the central hub for remote regions), and proxies HTTP and WebSocket traffic. Apps bound to `127.0.0.1` work; apps that use root-relative asset URLs may need a base path. Use SSH `-L` for raw TCP.
- **Template links**: Each template declares its own buttons for JetBrains, dashboards, or any URL. They appear on running workspaces and follow template versioning; see [docs/links.md](docs/links.md).
- **Stop / Start / Restart**: Stop deletes the pod but keeps the persistent volume; Start recreates it; Restart recreates the pod. Ephemeral workspaces lose their data on Stop.

If you used an administrator-managed key instead, load it once:
```bash
ssh-add ./poddy_key
```

### 7. Manage users, teams, and access
Use the dedicated **Users**, **Teams**, and **Templates** pages in the top bar. **Users** lists known identities and groups (admins also see team memberships). **Teams** creates and edits teams with the same table/autocomplete member editor. **Templates** searches by name, description, image, or version; select a template to edit its access. Owners, users, and team members are table rows with **Add** / **Remove** buttons.

### 8. Push a template (optional)
Apply a ConfigMap labeled `poddy.io/template=true` with `template.yaml` and `pod.yaml` keys, or install the separate `poddy-templates` chart. Add `poddy.io/template-version` and `poddy.io/template-active=true` to keep versions for rollback. The portal hot-reloads templates; no restart is required. See [docs/templates.md](docs/templates.md#pushing-templates-to-kubernetes).

### 9. Try OIDC + RBAC (optional)
A local Dex demo is included:

```bash
./scripts/dev-oidc.sh
```

The demo ships a default user set: `admin`/`ops` (global admins), `dev` (owner of `go`), `alice`/`bob` (backend), `carol` (frontend), `dave` (direct `go` user), and `erin` (base only). See [docs/oidc.md](docs/oidc.md#local-demo-with-dex) for the full table and [docs/rbac.md](docs/rbac.md).

## Development from source

Building the portal and workspace images is only needed for contributors. See
[docs/development.md](docs/development.md) for Go, Docker, and local image
instructions. The publish workflows build and push the GHCR images and Helm charts; manual
`docker push` is not part of the setup flow.

## Production deployment

Use the Helm charts for anything beyond a quick test. See [docs/deploy.md](docs/deploy.md) for:
- Installing the portal chart (portal + bastion Deployments) and templates chart with TLS, LoadBalancer SSH, and a stable host key.
- Scaling the portal and bastion independently, multi-team namespaces, RBAC, SSH keys, image pull secrets.
- Whether you need PostgreSQL and how to enable the [optional metadata mirror and audit log](docs/postgres-schema.md).

For a clean local k3s deployment with separate control-plane and workspace
namespaces, see [docs/k3s-test.md](docs/k3s-test.md).

## Custom templates & images

See [docs/templates.md](docs/templates.md) for:
- The template contract (`template.yaml` + `pod.yaml`).
- Built-in `base`, `go`, `node`, and `python` templates.
- Adding external templates (`-templates-dir`, the `poddy-templates` chart, or labeled ConfigMaps).
- Pushing a template at any time without restarting the portal.
- Versioning templates and rolling back by moving the active label.
- Building custom images on top of `ghcr.io/slimaineb/poddy-workspace-base`.
- Example: Node.js workspace in `examples/templates/node` and `examples/workspaces/node`.

## Repository layout

```
├── main.go                      # Portal entry point, config, wiring
├── cmd/poddy-bastion/           # Standalone SSH bastion entry point
├── internal/
│   ├── config/                  # Flags & env
│   ├── k8s/                     # Workspace manager, template ConfigMap watcher, PVC, port-forward
│   ├── templates/               # Template catalog, rendering, validation
│   ├── rbac/                    # ConfigMap-backed admins, teams, template access
│   ├── auth/                    # OIDC provider, sessions
│   ├── secure/                  # HMAC tokens, signed sessions, random helpers
│   ├── externalauth/            # OAuth2 providers, encrypted token store, refresh
│   ├── userstore/               # ConfigMap-backed OIDC user directory
│   ├── bastion/                 # SSH bastion (direct-tcpip → port-forward, owner checks)
│   ├── sshkeys/                 # authorized_keys parsing and owner mapping
│   ├── links/                   # Custom link validation and ConfigMap storage
│   ├── metadata/                # Optional PostgreSQL mirror, migrations, audit writer
│   ├── server/                  # HTTP API, auth, RBAC, user directory, links, terminal WS, VS Code launch
│   └── local/                   # Windows/WSL SSH config, key copy, VS Code launch
├── web/                         # Embedded UI (index.html, core.js, workspaces.js, ..., style.css)
├── scripts/
│   ├── poddy-ssh-start         # Entrypoint in workspace pods (writes authorized_keys, starts sshd)
│   ├── poddy-external-auth      # Print a refreshed external auth token in a workspace
│   ├── poddy-git-askpass        # Git askpass helper using the portal
│   └── dev-oidc.sh              # Local Dex + OIDC + RBAC demo
├── Dockerfile                   # Portal + bastion distroless build
├── Dockerfile.workspace-base    # Reusable SSH + dev user base image
├── Dockerfile.workspace-go      # Go toolchain on top of base
├── Dockerfile.workspace-node    # Node.js 22 toolchain on top of base
├── Dockerfile.workspace-python  # Python 3.11 + uv on top of base
├── examples/
│   ├── templates/node/          # Custom template example
│   └── workspaces/node/         # Custom image example
├── deploy/
│   ├── poddy.yaml              # Quick-start combined manifest (local only)
│   ├── dex/                     # Local Dex demo (config, compose, RBAC seed)
│   └── helm/
│       ├── poddy/              # Portal + bastion chart
│       └── poddy-templates/    # Independently managed template ConfigMaps
├── migrations/
│   ├── embed.go                 # Embeds migrations in the portal/bastion image
│   └── 0001_init.sql            # Optional PostgreSQL metadata schema
├── mkdocs.yml                   # MkDocs Material site config
├── requirements-docs.txt        # Docs build dependencies
└── docs/
    ├── index.md
    ├── getting-started.md
    ├── deploy.md
    ├── k3s-test.md
    ├── templates.md
    ├── external-auth.md
    ├── links.md
    ├── oidc.md
    ├── rbac.md
    ├── architecture.md
    ├── data-model.md
    ├── postgres-schema.md
    └── development.md
```

## Authentication

Two modes are available and can be combined:

- **OIDC / SSO** (recommended): set `PODDY_OIDC_ISSUER`, `PODDY_OIDC_CLIENT_ID` (and optionally `PODDY_OIDC_CLIENT_SECRET`). Users sign in through your identity provider; sessions are signed cookies. Restrict access with `PODDY_OIDC_ALLOWED_GROUPS` / `PODDY_OIDC_ALLOWED_USERS`.
- **Shared token**: set `PODDY_TOKEN` (or `-token`). Useful for local development and API automation (`Authorization: Bearer <token>`). The token remains valid when OIDC is enabled.

The login page shows **Continue with SSO** when OIDC is configured, and the token form when a token is configured. The topbar shows the signed-in user, their role, and a sign-out button.

See [docs/oidc.md](docs/oidc.md) for provider setup, claims, and troubleshooting.

## External auth for workspaces

External auth is separate from portal login. Configure OAuth2 providers (GitHub, GitLab, Gitea, Azure DevOps, Bitbucket, or generic OIDC), and templates that declare `externalAuth:` receive a refreshed token inside the workspace:

```yaml
externalAuth:
  - id: github
  - id: gitlab
    optional: true
```

Users connect accounts under **Connections**. Required providers block workspace creation with HTTP `409` until connected. The portal stores tokens encrypted in the `poddy-external-auth` Secret, refreshes them server-side, and exposes `poddy-external-auth token <id>` plus a Git askpass helper in the base image. See [docs/external-auth.md](docs/external-auth.md).

## RBAC

Roles are stored in the `poddy-rbac` ConfigMap in the workspace namespace:

- **Global admins** see and manage every workspace and template, and manage teams.
- **Template owners** choose the users and teams allowed to use their templates (admins choose owners).
- **Teams** group users; a team can be assigned to any number of templates.
- **Regular users** see their own workspaces and the templates assigned to them.

A template without an access entry is public. Workspace ownership is stored in the `poddy.io/owner` annotation. Trusted local mode (no token and no OIDC) is a global admin. A shared token has one identity and is not admin unless `auth.tokenAdmin=true`. See [docs/rbac.md](docs/rbac.md).

## Template links

Each template declares its own buttons in `template.yaml` under `links:`. They are rendered on workspaces created from that template and are versioned with it. URLs support placeholders such as `{name}`, `{alias}`, `{sshHost}`, `{sshPort}`, and `{user}`. See [docs/links.md](docs/links.md).

## Security notes

- With a **shared token**, everyone shares the same identity (`token`) and sees the same workspaces. The token is not a global admin by default; set `auth.tokenAdmin=true` only for trusted automation or single-operator deployments. Prefer OIDC for teams.
- **SSH keys are scoped to owners.** The preferred flow is **SSH keys** in the portal: the server binds the key to the signed-in identity, and the key comment is ignored. Administrator-managed keys in `ssh.authorizedKeys` are bound by their comment (`alice@example.com` or `user:alice@example.com`); `admin` or `*` are break-glass keys. The bastion rejects connections to workspaces owned by anyone else.
- Workspace pods receive only their owner's keys plus admin keys, through a read-only per-workspace Secret mounted at `/etc/poddy/ssh/authorized_keys`; they no longer receive every global key.
- `ssh.allowUnmappedKeys: true` restores the legacy shared-gateway behavior (any key can reach any workspace). It is disabled by default and enabled automatically only in trusted local mode with no token and no OIDC.
- In local editor mode, the portal marks the SSH keys it manages as admin keys so local SSH/VS Code keeps working with token or OIDC auth. Cluster deployments run with `-editor=` and do not add local keys.
- Template access limits who can create workspaces from a template, not who can SSH into an existing workspace. SSH owner checks cover that separately.
- For hard tenant isolation, use separate namespaces/releases. The SSH check prevents cross-owner access inside one portal, but pods in a shared namespace still share network and storage classes.
- Workspace containers run `sshd` as root (required to bind port 22 and switch to `dev`). The portal itself runs non-root (UID 65532), read-only rootfs, dropped capabilities.
- Kubernetes RBAC: the portal's Role is scoped to the workspace namespace; it cannot touch cluster-wide resources. The bastion has a separate ServiceAccount with only pod read access and port-forward.
- Use HTTPS ingress in production; the cookie is `Secure` when TLS is detected.

## CI and documentation

GitHub Actions workflows live in `.github/workflows/`:

- **CI** runs Go formatting/vet/tests, JavaScript syntax checks, Helm lint/template, and Docker image builds.
- **Publish images** builds and pushes the portal/bastion image, workspace base, and workspace images to GHCR on pushes to `main`/`master` and `v*` tags.
- **Pages** builds the MkDocs Material site and deploys it to GitHub Pages.

Published images use the repository owner's GHCR namespace: `ghcr.io/slimaineb/poddy`, `ghcr.io/slimaineb/poddy-workspace-base`, `ghcr.io/slimaineb/poddy-workspace`, `ghcr.io/slimaineb/poddy-workspace-node`, and `ghcr.io/slimaineb/poddy-workspace-python`. `latest` follows `main`; `v*` tags get semver tags. Make the GHCR packages public for the quick start; private workspace images need `imagePullSecrets` in the workspace pod template.

Documentation lives under `docs/` and is configured by `mkdocs.yml`. The live site is <https://slimaineb.github.io/poddy/>. Start at [`docs/index.md`](docs/index.md) or go straight to [Getting started](docs/getting-started.md). Preview locally with `pip install -r requirements-docs.txt && mkdocs serve`. Enable Pages once in **Settings → Pages → Source: GitHub Actions**; the default `GITHUB_TOKEN` cannot create the Pages site. Keep the published docs self-contained: links to files outside `docs/` (for example `deploy/`) will not resolve on the Pages site.

## License

MIT