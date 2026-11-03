# poddy

Browser-accessible Kubernetes development workspaces with an SSH bastion. Open VS Code on Windows/WSL connected to a pod via SSH — no kubeconfig required for users.

## Features

- **Web portal**: Create, stop, start, restart, delete, and list workspaces; browser terminal (`xterm.js`).
- **Authentication**: OIDC / SSO (authorization code + PKCE) with group/user allowlists, or a shared access token for simple setups and API automation.
- **RBAC**: global admins, template owners, teams, and per-template user/team access. Workspaces are owned by their creator; admins see everything, users see their own. Users, teams, and templates each have a dedicated page; data is stored in a ConfigMap.
- **Extensible templates**: YAML templates define pod specs, parameters, and images. Built-in `base` and `go` templates; add your own via `-templates-dir`, the separate `poddy-templates` Helm chart, or labeled ConfigMaps. The portal hot-reloads templates without restarting, and keeps multiple versions for one-label rollback.
- **External auth**: connect GitHub/GitLab/Gitea/Azure DevOps/Bitbucket OAuth2 accounts and inject refreshed tokens into workspaces. Templates declare required providers; the portal handles refresh, Git askpass, and per-user connections.
- **SSH bastion**: Single entry point (`poddy-ssh-*` via ProxyJump) port-forwards to workspace pods. Keys are bound to workspace owners; users only need an SSH key.
- **VS Code launch**: One-click opens Remote-SSH (`vscode://`) from the workspace card. In local editor mode the portal can also launch Windows VS Code directly, write `~/.ssh/config`, and copy the portal key.
- **Persistent volumes**: PVC per workspace; ephemeral storage when empty. Stop keeps the volume; stopped specs are remembered in a ConfigMap.
- **RBAC-scoped**: Portal runs as a non-root ServiceAccount with a Role in the workspace namespace (pods, exec, port-forward, PVCs, ConfigMaps, Secrets).
- **Helm charts**: Separate portal and templates releases, with TLS ingress, LoadBalancer SSH, shared host key, PDB, resource limits, and security context.
- **No database**: Kubernetes is the source of truth; templates and RBAC are ConfigMaps.

## Architecture

```
Browser ──────────► Portal (:8080) ──────────► Kubernetes API (workspace namespace)
     │                     ▲
     │                     │
     │              SSH bastion (:2222)
     │                     ▲
     │                     │
VS Code / ssh ◄──── ProxyJump ◄─── Port-forward ◄─── Workspace pod :22
```

The portal is stateless; scale replicas behind a LoadBalancer. The bastion shares a host key across replicas.

## Quick start (local k3s + WSL)

### Prerequisites
- Go 1.26+
- Docker
- kubectl + k3s (running)
- Windows VS Code with **Remote-SSH** extension

### 1. Build workspace images
```bash
docker build -f Dockerfile.workspace-base -t poddy/workspace-base:latest .
docker build -f Dockerfile.workspace -t poddy/workspace:latest .
```
Import them into k3s containerd:
```bash
docker save poddy/workspace-base:latest poddy/workspace:latest | sudo k3s ctr images import -
```

### 2. Build the portal
```bash
go build -o poddy .
```

### 3. Run the portal (WSL)
```bash
./poddy -context local -namespace poddy -token=secret \
  -ssh-addr=127.0.0.1:2222 -ssh-public-host=127.0.0.1 -ssh-public-port=2222
```
- `-context local` uses the `local` kubeconfig context (your k3s).
- `-namespace poddy` is the workspace namespace.
- `-token=secret` is the shared access token (use a strong random string in prod).
- The SSH bastion listens on `127.0.0.1:2222`; `-ssh-public-host/port` is what users connect to.
- In local editor mode, the portal marks the SSH keys it manages as admin keys, so the **SSH** and **VS Code** buttons keep working with token or OIDC login. Additional keys passed with `-ssh-keys` / `PODDY_SSH_KEYS` must be commented `user:<identity>` or `admin`.

### 4. Open the UI
Open `http://127.0.0.1:8090` in your browser. Enter the token `secret`.

### 5. Create a workspace
Click **New workspace**, pick **Base** or **Go**, set parameters, **Create**. Wait for **Running**.

### 6. Connect
- **Terminal**: Click the terminal icon for a browser shell.
- **SSH**: Click **SSH** to copy a ready-to-use SSH config (ProxyJump through the bastion).
- **VS Code**: Click **VS Code** to open Remote-SSH on the workspace user's home (`/home/dev`). In cluster mode this uses `vscode://` and needs the SSH config from the **SSH** dialog once; that config covers all workspaces. In local editor mode it launches VS Code directly. On Windows, save the config and key under `C:\Users\<you>\.ssh\`, not only in WSL.
- **Template links**: Each template declares its own buttons for JetBrains, dashboards, or any URL. They appear on running workspaces and follow template versioning; see [docs/links.md](docs/links.md).
- **Stop / Start / Restart**: Stop deletes the pod but keeps the persistent volume; Start recreates it; Restart recreates the pod. Ephemeral workspaces lose their data on Stop.

### 7. Manage users, teams, and access
Use the dedicated **Users**, **Teams**, and **Templates** pages in the top bar. **Users** lists known identities and groups (admins also see team memberships). **Teams** creates and edits teams with the same table/autocomplete member editor. **Templates** searches by name, description, image, or version; select a template to edit its access. Owners, users, and team members are table rows with **Add** / **Remove** buttons.

### 8. Push a template (optional)
Local runs can use `-templates-dir ./my-templates`. In Kubernetes, apply a ConfigMap labeled `poddy.io/template=true` with `template.yaml` and `pod.yaml` keys, or install the separate `poddy-templates` chart. Add `poddy.io/template-version` and `poddy.io/template-active=true` to keep versions for rollback. The portal hot-reloads templates; no restart is required. See [docs/templates.md](docs/templates.md#pushing-templates-to-kubernetes).

### 9. Try OIDC + RBAC (optional)
A local Dex demo is included:

```bash
kubectl --context local create namespace poddy   # once
./scripts/dev-oidc.sh
```

The demo ships a default user set: `admin`/`ops` (global admins), `dev` (owner of `go`), `alice`/`bob` (backend), `carol` (frontend), `dave` (direct `go` user), and `erin` (base only). See [docs/oidc.md](docs/oidc.md#local-demo-with-dex) for the full table and [docs/rbac.md](docs/rbac.md).

## Production deployment

Use the Helm charts for anything beyond a quick test. See [docs/deploy.md](docs/deploy.md) for:
- Installing the portal and templates charts with TLS, LoadBalancer SSH, and a stable host key.
- Scaling replicas, multi-team namespaces, RBAC, SSH keys, image pull secrets.
- Why there is no Postgres and when you might add one.

For a clean local k3s deployment with separate control-plane and workspace
namespaces, see [docs/k3s-test.md](docs/k3s-test.md).

## Custom templates & images

See [docs/templates.md](docs/templates.md) for:
- The template contract (`template.yaml` + `pod.yaml`).
- Built-in `base` / `go` templates.
- Adding external templates (`-templates-dir`, the `poddy-templates` chart, or labeled ConfigMaps).
- Pushing a template at any time without restarting the portal.
- Versioning templates and rolling back by moving the active label.
- Building custom images on top of `poddy/workspace-base`.
- Example: Node.js workspace in `examples/templates/node` and `examples/workspaces/node`.

## Repository layout

```
├── main.go                      # Entry point, config, wiring
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
│   ├── server/                  # HTTP API, auth, RBAC, user directory, links, terminal WS, VS Code launch
│   └── local/                   # Windows/WSL SSH config, key copy, VS Code launch
├── web/                         # Embedded UI (index.html, core.js, workspaces.js, ..., style.css)
├── scripts/
│   ├── poddy-ssh-start         # Entrypoint in workspace pods (writes authorized_keys, starts sshd)
│   ├── poddy-external-auth      # Print a refreshed external auth token in a workspace
│   ├── poddy-git-askpass        # Git askpass helper using the portal
│   └── dev-oidc.sh              # Local Dex + OIDC + RBAC demo
├── Dockerfile                   # Portal distroless build
├── Dockerfile.workspace-base    # Reusable SSH + dev user base image
├── Dockerfile.workspace         # Go toolchain on top of base
├── examples/
│   ├── templates/node/          # Custom template example
│   └── workspaces/node/         # Custom image example
├── deploy/
│   ├── poddy.yaml              # Quick-start single-namespace manifest
│   ├── dex/                     # Local Dex demo (config, compose, RBAC seed)
│   └── helm/
│       ├── poddy/              # Portal chart
│       └── poddy-templates/    # Independently managed template ConfigMaps
└── docs/
    ├── deploy.md
    ├── k3s-test.md
    ├── external-auth.md
    ├── links.md
    ├── oidc.md
    ├── rbac.md
    └── templates.md
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

A template without an access entry is public. Workspace ownership is stored in the `poddy.io/owner` annotation. The shared token and trusted local mode act as global admins. See [docs/rbac.md](docs/rbac.md).

## Template links

Each template declares its own buttons in `template.yaml` under `links:`. They are rendered on workspaces created from that template and are versioned with it. URLs support placeholders such as `{name}`, `{alias}`, `{sshHost}`, `{sshPort}`, and `{user}`. See [docs/links.md](docs/links.md).

## Security notes

- With a **shared token**, anyone holding it is a **global admin** and can create/delete workspaces and exec/port-forward. Prefer OIDC for team deployments; keep the token only for automation.
- **SSH keys are scoped to owners.** The comment on an authorized key maps it to a workspace owner: `alice@example.com` or `user:alice@example.com`. For non-email identities use the `user:` prefix (for example `user:token` for token auth). The bastion rejects connections to workspaces owned by anyone else. Keys commented `admin` or `*` are break-glass keys with access to every workspace. Keys without a recognized comment are denied.
- Workspace pods receive only the owner's key plus admin keys in `SSH_AUTHORIZED_KEY`; they no longer receive every global key.
- `ssh.allowUnmappedKeys: true` restores the legacy shared-gateway behavior (any key can reach any workspace). It is disabled by default and enabled automatically only in trusted local mode with no token and no OIDC.
- In local editor mode, the portal marks the SSH keys it manages as admin keys so local SSH/VS Code keeps working with token or OIDC auth. Cluster deployments run with `-editor=` and do not add local keys.
- Template access limits who can create workspaces from a template, not who can SSH into an existing workspace. SSH owner checks cover that separately.
- For hard tenant isolation, use separate namespaces/releases. The SSH check prevents cross-owner access inside one portal, but pods in a shared namespace still share network and storage classes.
- Workspace containers run `sshd` as root (required to bind port 22 and switch to `dev`). The portal itself runs non-root (UID 65532), read-only rootfs, dropped capabilities.
- Kubernetes RBAC: the portal's Role is scoped to the workspace namespace; it cannot touch cluster-wide resources.
- Use HTTPS ingress in production; the cookie is `Secure` when TLS is detected.

## Documentation and GitHub Pages

All documentation is plain Markdown under `docs/` and renders directly on GitHub. GitHub Pages is used only for this documentation site; the portal itself (Go server, WebSocket terminal, SSH bastion, Kubernetes API) stays on Kubernetes.

When publishing, set the Pages source to `/docs`. Keep the published docs self-contained: links to files outside `docs/` (for example `deploy/`) will not resolve on the Pages site.

## License

MIT