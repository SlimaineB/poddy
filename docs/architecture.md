# Architecture

poddy is a stateless Go portal plus a set of Kubernetes workspace pods. It has
no database: Kubernetes is the control plane and the source of truth.

## High-level view

```text
                         ┌──────────────────────────────┐
                         │           Browser            │
                         └──────────────┬───────────────┘
                                        │ HTTPS
                         ┌──────────────▼───────────────┐
                         │        poddy portal          │
                         │  HTTP API + embedded web UI  │
                         └───────┬──────────────┬───────┘
                                 │              │
                    Kubernetes API              │ SSH bastion
                                 │              │
              ┌──────────────────▼───┐   ┌──────▼─────────────┐
              │ Workspace namespace  │   │ ProxyJump / TCP    │
              │ pods, PVCs, ConfigMaps│  │ port-forward       │
              └──────────────────────┘   └──────┬─────────────┘
                                                │
                                      ┌─────────▼─────────┐
                                      │ Workspace pod :22 │
                                      │ sshd + dev user   │
                                      └───────────────────┘
```

## Components

| Component | Responsibility | Where it runs |
|-----------|----------------|---------------|
| Portal | HTTP API, auth, RBAC, workspace lifecycle, embedded UI, terminal WebSocket | Deployment in the control namespace |
| SSH bastion | Accepts user SSH connections and port-forwards to workspace pods | Same process as the portal |
| Workspace pods | `sshd`, a non-root `dev` user, tools, and the user's home volume | Workspace namespace |
| Templates | YAML contract for pod spec, parameters, image, links, and external auth | ConfigMaps or a local directory |
| RBAC store | Admins, teams, and template access | ConfigMap |
| Workspace state | Rendered specs of stopped workspaces | ConfigMap |
| User directory | OIDC identities seen by the portal | ConfigMap |
| External auth store | Encrypted OAuth2 tokens per user and provider | Secret |

## Request flows

### Create a workspace

1. The browser calls `POST /api/workspaces` on the portal.
2. The portal authenticates the request and checks template access.
3. It renders the template's `pod.yaml` with the chosen parameters.
4. It creates the pod and, when requested, a PVC in the workspace namespace.
5. The owner is written to the `poddy.io/owner` annotation.
6. The portal watches pods and reports their phase to the UI.

### Browser terminal

1. The browser opens a WebSocket to the portal.
2. The portal checks authentication and workspace ownership.
3. It uses the Kubernetes exec API to start a shell in the workspace pod.
4. Terminal I/O is streamed through the WebSocket.

### SSH and VS Code

1. The user connects to the bastion with an SSH key.
2. The key comment maps the key to an owner (`alice@example.com` or
   `user:alice`).
3. The bastion checks that the owner matches the target workspace.
4. It port-forwards to the pod's port 22 through the Kubernetes API.
5. VS Code connects through `ProxyJump` using the same bastion.

Keys commented `admin` or `*` are break-glass keys. Unmapped keys are denied by
default.

## Portal internals

The portal is a single Go binary with an embedded web UI:

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

## State and storage

poddy deliberately avoids an external database:

- **Workspaces** are pods with `app.kubernetes.io/name=poddy`.
- **Ownership** is the `poddy.io/owner` annotation.
- **Stopped workspace specs** live in the `poddy-state` ConfigMap.
- **Templates** are ConfigMaps labeled `poddy.io/template=true`.
- **RBAC** lives in the `poddy-rbac` ConfigMap.
- **OIDC users** live in the `poddy-users` ConfigMap.
- **External auth tokens** live in the `poddy-external-auth` Secret, encrypted
  with AES-GCM when a key is configured.
- **Persistent data** lives in one PVC per persistent workspace.

See [Data model](data-model.md) for the object-level details.

## Scaling and availability

The portal is stateless, so it can run multiple replicas behind a Service or
LoadBalancer. Session cookies are signed, and the SSH bastion uses a shared
host key so connections work across replicas.

Workspace pods are independent. The portal's Kubernetes Role is scoped to the
workspace namespace; it cannot manage cluster-wide resources.

## Security boundaries

- The portal runs as non-root with a read-only root filesystem and dropped
  capabilities.
- Workspace containers run `sshd` as root only to bind port 22 and switch to the
  `dev` user.
- SSH keys are scoped to workspace owners. Template access controls who can
  create a workspace, not who can SSH into an existing one.
- For hard tenant isolation, use separate namespaces or separate releases.
- Use HTTPS in production; the session cookie is marked `Secure` when TLS is
  detected.
