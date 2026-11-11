# Guided tour

Welcome to poddy. Here is how everything fits together, without magic or hidden
commands.

## The principle

Poddy creates **Kubernetes workspaces** (isolated development machines) that
users operate from a **web portal**. The user never needs `kubectl` or a
kubeconfig. They create a workspace from the browser, then connect to it over
SSH (VS Code, Cursor, PyCharm, terminal, or browser).

## The three components

| Component | Role | Where it runs |
|-----------|------|---------------|
| **Portal** | UI, create/stop/start workspaces, authentication, RBAC | Deployment `poddy` |
| **SSH bastion** | SSH entry point (`:2222`), connects the user to the workspace via an agent | Deployment `poddy-bastion` |
| **Workspace** | Development container (`ssh` on port 22, `/home/dev` volume) | Pod in the workspace namespace |

The portal and bastion are built from the same image. They are **stateless** and
sit behind a LoadBalancer or an Ingress.

## The user flow

```
Browser ─────────► Portal ──► Kubernetes API (creates the workspace pod)
                    │
                    └── signed-in user (OIDC or shared token)

IDE / SSH ───────► Bastion ──► Agent sidecar in the workspace ──► sshd (:22)
```

When you click **VS Code**, the portal generates a two-minute signed ticket, the
Poddy extension enrolls your machine, writes your `~/.ssh/config`, and opens the
editor. You never touch an SSH key by hand.

## Authentication: OIDC (recommended) or shared token

**OIDC** is the recommended mode for a team. Connect your identity provider
(Keycloak, Okta, Entra ID, Google, Dex for demos…). Users sign in with their
work account. No shared token circulates in the browser.

**Shared token** is a single password for the portal. Handy for automation or
testing, but not recommended for multi-user production.

Configure OIDC in production:
```yaml
oidc:
  enabled: true
  existingSecret: poddy-oidc
  issuer: https://sso.example.com/realms/dev
  clientId: poddy
  redirectUrl: https://poddy.example.com/oidc/callback
  scopes: "openid,profile,email,groups"
  allowedGroups: ["devs"]
```

The secret `poddy-oidc` contains `oidc-client-id`, `oidc-client-secret`, and
`oidc-session-key`.

## PostgreSQL: optional

Kubernetes is the **source of truth** (workspaces, templates, RBAC, quotas,
stopped specs). You do **not** need PostgreSQL to run poddy.

PostgreSQL adds:
- a queryable audit log,
- a durable copy of metadata (RBAC, users, quotas),
- snapshots for disaster recovery.

If you want it:
```yaml
database:
  existingSecret: poddy-database
  urlKey: database-url
  autoMigrate: true
```

The secret `poddy-database` contains `database-url`.

## Install in production (without the headache)

1. **Prerequisites**: a Kubernetes cluster (k3s, EKS…), Helm 3.13+, a default
   StorageClass for workspace PVCs, an Ingress controller for HTTPS.
2. **Create the portal secret**:
   ```bash
   kubectl create secret generic poddy-auth \
     --from-literal=token="$(openssl rand -hex 32)" \
     -n poddy-system
   ```
3. **Configure OIDC** (recommended):
   ```bash
   kubectl create secret generic poddy-oidc \
     --from-literal=oidc-client-id=poddy \
     --from-literal=oidc-client-secret="..." \
     --from-literal=oidc-session-key="$(openssl rand -hex 32)" \
     -n poddy-system
   ```
4. **Install the chart** (published on GHCR, no repo checkout required):
   ```bash
   helm upgrade --install poddy oci://ghcr.io/slimaineb/charts/poddy \
     -n poddy-system --create-namespace --version 0.1.3 -f values.yaml
   ```
5. **Install the templates** (optional, separate release):
   ```bash
   helm upgrade --install poddy-templates oci://ghcr.io/slimaineb/charts/poddy-templates \
     -n poddy-workspaces --version 0.1.3 -f templates-values.yaml
   ```

Minimal `values.yaml`:
```yaml
image:
  repository: ghcr.io/slimaineb/poddy
  tag: "latest"

auth:
  existingSecret: poddy-auth
  tokenAdmin: false

oidc:
  enabled: true
  existingSecret: poddy-oidc
  issuer: https://sso.example.com/realms/dev
  clientId: poddy
  redirectUrl: https://poddy.example.com/oidc/callback
  scopes: "openid,profile,email,groups"

ssh:
  enabled: true
  publicHost: ssh.dev.example.com
  publicPort: 2222

bastion:
  enabled: true
  replicaCount: 1

workspaceNamespace: poddy-workspaces
createWorkspaceNamespace: true

ingress:
  enabled: true
  className: nginx
  hosts:
    - host: poddy.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: poddy-tls
      hosts:
        - poddy.example.com
```

## After installation

1. Open the portal (`https://poddy.example.com`), sign in.
2. Go to **SSH keys**, add your public key.
3. Create a workspace (**Base**, **Go**, **Node.js**, or **Python**).
4. Once in **Running**, click **VS Code** (or your IDE of choice).
5. Your workspaces stop and start automatically with the **auto-stop schedule**
   you configured.

## What not to do in production

- Do not use the shared token for the whole team (use OIDC).
- Do not expose the metrics port (`9090`) publicly.
- Do not rely on the ephemeral host key if you run multiple bastion replicas
  (use `ssh.existingHostKeySecret`).
- Do not delete the workspace namespace with `helm uninstall` if you want to
  keep the data (`createWorkspaceNamespace: false` + create the namespace by
  hand).

## Go further

- [Kubernetes deployment](deploy.md): TLS, LoadBalancer, multi-region, scaling.
- [OIDC / SSO](oidc.md): provider configuration, groups, sessions.
- [RBAC and ownership](rbac.md): admins, teams, template access.
- [Workspace templates](templates.md): add your own workspace image.
- [Volumes and backups](volumes.md): S3, backup/restore workspaces.
- [Architecture](architecture.md): request flow and components.
