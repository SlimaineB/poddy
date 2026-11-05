# Architecture

poddy is a stateless Go portal plus a set of Kubernetes workspace pods. It does
not require a database: Kubernetes is the control plane and the source of truth.
An optional PostgreSQL mirror is available for metadata durability, disaster
recovery, and an audit trail.

## High-level view

```text
┌──────────┐   HTTPS   ┌──────────────────────────┐
│ Browser  │──────────►│        poddy portal      │
└──────────┘           │  HTTP API + embedded UI  │
                       └────────────┬─────────────┘
                                    │ Kubernetes API
                                    ▼
                       ┌──────────────────────────┐
                       │    workspace namespace   │
                       │ pods, PVCs, ConfigMaps   │
                       └────────────▲─────────────┘
                                    │ port-forward
┌───────────────┐   SSH   ┌─────────┴─────────────┐
│ VS Code / ssh │────────►│     poddy-bastion     │
└───────────────┘         │  SSH :2222, ProxyJump │
                          └───────────────────────┘
```

The portal and bastion are separate Deployments built from the same container
image. The portal never listens for SSH in cluster mode; the bastion has no HTTP
API and only needs pod read access plus port-forward.

## Components

| Component | Responsibility | Where it runs |
|-----------|----------------|---------------|
| Portal | HTTP API, auth, RBAC, workspace lifecycle, embedded UI, terminal WebSocket | Deployment in the control namespace |
| SSH bastion | Accepts user SSH connections and port-forwards to workspace pods | Separate Deployment in the control namespace, same image, `/poddy-bastion` |
| Workspace pods | `sshd`, a non-root `dev` user, tools, and the user's home volume | Workspace namespace |
| Templates | YAML contract for pod spec, parameters, image, links, and external auth | ConfigMaps or a local directory |
| RBAC store | Admins, teams, and template access | ConfigMap |
| Workspace state | Rendered specs of stopped workspaces | ConfigMap |
| User directory | OIDC identities seen by the portal | ConfigMap |
| External auth store | Encrypted OAuth2 tokens per user and provider | Secret |
| Metadata mirror (optional) | Durable copy of control-plane metadata and append-only audit events | External PostgreSQL |

## Request flows

### Create a workspace

1. The browser calls `POST /api/workspaces` on the portal.
2. The portal authenticates the request, checks template access, and prefixes
   the name with the authenticated username when authentication is enabled.
3. It renders the template's `pod.yaml` with the chosen parameters.
4. It creates the pod and, when requested, a PVC in the workspace namespace.
5. The owner is written to the `poddy.io/owner` annotation.
6. If PostgreSQL is enabled, the portal mirrors the workspace snapshot and
   records a `workspace.create` audit event.
7. The portal watches pods and reports their phase to the UI.

### Browser terminal

1. The browser opens a WebSocket to the portal.
2. The portal checks authentication and workspace ownership.
3. It uses the Kubernetes exec API to start a shell in the workspace pod.
4. Terminal I/O is streamed through the WebSocket.
5. If PostgreSQL is enabled, the portal records `workspace.terminal.open`.

### SSH and VS Code

1. The user connects to the `poddy-bastion` Service with an SSH key.
2. The key comment maps the key to an owner (`alice@example.com` or
   `user:alice`).
3. The bastion checks that the owner matches the target workspace.
4. It port-forwards to the pod's port 22 through the Kubernetes API.
5. If PostgreSQL is enabled, the bastion records `ssh.connect`.
6. VS Code connects through `ProxyJump` using the same bastion.

### Browser VS Code

1. The workspace pod runs an optional `code-server` sidecar bound to
   `127.0.0.1:3000`.
2. The browser opens `/api/workspaces/<name>/code/`.
3. The portal authenticates the user, checks workspace ownership, and
   port-forwards to port 3000 through the Kubernetes API.
4. The portal reverse-proxies HTTP and WebSocket traffic, preserving the
   OpenVSCode base path.

In local mode, the portal process can start the same bastion in-process on
`-ssh-addr`. Helm deployments run it as a separate Deployment.

Keys commented `admin` or `*` are break-glass keys. Unmapped keys are denied by
default.

## Portal and bastion internals

poddy ships two Go binaries from one module:

- `poddy` (`main.go`) runs the portal and embeds the web UI. In local mode it can
  also start the bastion in-process when `-ssh-addr` is set.
- `poddy-bastion` (`cmd/poddy-bastion`) runs only the SSH bastion for cluster
  deployments.

| Package | Role |
|---------|------|
| `internal/config` | Flags and environment configuration |
| `internal/server` | HTTP API, auth, RBAC checks, terminal WebSocket, VS Code launch |
| `internal/k8s` | Workspace lifecycle, pod rendering, PVCs, state ConfigMap, port-forward |
| `internal/templates` | Template catalog, validation, rendering, hot reload |
| `internal/rbac` | Admins, teams, and template access |
| `internal/auth` | OIDC authorization code + PKCE, sessions |
| `internal/externalauth` | OAuth2 providers, encrypted token store, refresh |
| `internal/userstore` | OIDC user directory |
| `internal/bastion` | SSH bastion and owner checks |
| `internal/sshkeys` | `authorized_keys` parsing and key-to-owner mapping |
| `internal/links` | Template link validation |
| `internal/metadata` | Optional PostgreSQL mirror, migrations, and audit writer |
| `cmd/poddy-bastion` | Standalone bastion entry point |

## State and storage

poddy does not require an external database. Kubernetes is the default source of
truth:

- **Workspaces** are pods with `app.kubernetes.io/name=poddy`.
- **Ownership** is the `poddy.io/owner` annotation.
- **Stopped workspace specs** live in the `poddy-state` ConfigMap.
- **Templates** are ConfigMaps labeled `poddy.io/template=true`.
- **RBAC** lives in the `poddy-rbac` ConfigMap.
- **OIDC users** live in the `poddy-users` ConfigMap.
- **External auth tokens** live in the `poddy-external-auth` Secret, encrypted
  with AES-GCM when a key is configured.
- **Persistent data** lives in one PVC per persistent workspace.

An optional PostgreSQL mirror stores RBAC, OIDC users, templates, workspaces,
and audit events. When `PODDY_DATABASE_URL` is set, the portal syncs the mirror
at startup and after writes; the bastion writes `ssh.connect` audit events.
Kubernetes remains authoritative and PostgreSQL is never read for access
decisions. It stores control-plane metadata only; PVC contents still need volume
snapshots. See [PostgreSQL schema](postgres-schema.md) and
[Data model](data-model.md) for the object-level details.

### Cluster loss

Losing the cluster loses ConfigMaps, Secrets, and PVCs. A PostgreSQL backup can
restore metadata, but workspace files are only recoverable from PVC snapshots or
another storage backup. Test both restore paths independently.

## Scaling and availability

The portal and bastion scale independently:

- **Portal** (`replicaCount`) is stateless and can run multiple replicas behind a
  Service or LoadBalancer. Session cookies are signed.
- **Bastion** (`bastion.replicaCount`) scales on SSH connections. It uses a shared
  host key so clients see the same host key across replicas. Set
  `ssh.existingHostKeySecret` when running more than one bastion replica.
- **Local mode** runs both processes in one binary, which is convenient for
  development but not how Helm deploys them.

Workspace pods are independent. The portal's Kubernetes Role is scoped to the
workspace namespace; the bastion's Role is narrower still and only grants pod
read access plus port-forward.

## Security boundaries

- Both portal and bastion run as non-root with a read-only root filesystem and
  dropped capabilities.
- The portal and bastion use separate ServiceAccounts. The bastion's Role cannot
  read ConfigMaps or Secrets through the API and cannot manage workspace
  lifecycle.
- Workspace containers run `sshd` as root only to bind port 22 and switch to the
  `dev` user.
- SSH keys are scoped to workspace owners. Template access controls who can
  create a workspace, not who can SSH into an existing one.
- For hard tenant isolation, use separate namespaces or separate releases.
- Use HTTPS in production; the session cookie is marked `Secure` when TLS is
  detected.
