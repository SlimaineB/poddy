# poddy

<div class="hero" markdown>

**Your developers get a Linux machine. You keep the cluster.**

poddy turns Kubernetes into a self-service development platform: persistent
workspaces, one-click IDEs, SSO, ownership, quotas, templates, volumes, and
backups. Users never need `kubectl` or a kubeconfig.

[Quick start](getting-started.md){ .md-button .md-button--primary }
[See how it works](guided-tour.md){ .md-button }
[Run the full demo](demo.md){ .md-button }

</div>

## Why teams choose poddy

<div class="grid cards" markdown>

-   :material-account-key-outline:{ .lg .middle } **No Kubernetes homework**

    ---

    Developers create a workspace in the browser and start working. Cluster
    credentials, namespaces, pod specs, and SSH plumbing stay with the platform
    team.

-   :material-laptop:{ .lg .middle } **Their IDE, unchanged**

    ---

    Open a browser terminal, VS Code Web, VS Code/Cursor Remote-SSH, PyCharm
    through JetBrains Gateway, or JupyterLab. poddy prepares the connection for
    them.

-   :material-harddisk:{ .lg .middle } **Real workspaces, not demos**

    ---

    Each workspace has a persistent home volume, configurable CPU, memory, and
    storage, and can be stopped and resumed without losing files.

-   :material-shield-check:{ .lg .middle } **Built for teams**

    ---

    OIDC/SSO, owners, teams, per-template access, quotas, audit events, and
    owner-scoped SSH. A shared token is available for local use, not forced on
    production.

-   :material-puzzle-outline:{ .lg .middle } **Bring your own everything**

    ---

    Use the built-in images or your own. Templates are plain YAML with images,
    parameters, sidecars, ports, links, and external-auth requirements.

-   :material-kubernetes:{ .lg .middle } **Kubernetes-native**

    ---

    Any conforming cluster, Helm charts, least-privilege ServiceAccounts,
    Prometheus metrics, optional S3 backups, and no proprietary control plane.

</div>

## What poddy gives you

=== "For developers"

    - A workspace in seconds, not a ticket.
    - A persistent `/home/dev` with the tools their template provides.
    - Browser terminal and browser VS Code with no local setup.
    - One-click Remote-SSH in VS Code or Cursor, JetBrains Gateway, and JupyterLab.
    - GitHub/GitLab credentials that refresh instead of expiring mid-session.

=== "For platform teams"

    - One portal for every workspace, with search, topology, and multi-region maps.
    - Templates that standardise images, resources, sidecars, and access.
    - OIDC, RBAC, quotas, audit, and workspace ownership.
    - Namespace-scoped permissions instead of per-user cluster access.
    - Registry mirrors, private image pull secrets, and optional S3 volume backups.

=== "For security teams"

    - No kubeconfig or cluster credentials on developer laptops.
    - SSO-backed identities and owner-scoped SSH.
    - Encrypted external-auth tokens, shares, and environment secrets.
    - A queryable audit trail for workspace, access, and terminal events.
    - Non-root portal, read-only root filesystem, dropped capabilities, and a
      separate bastion ServiceAccount.

## The fastest paths

<div class="grid cards" markdown>

-   :material-rocket-launch-outline:{ .lg .middle } **Create your first workspace**

    ---

    Install the portal and default templates on an existing cluster, then open
    the portal and create a workspace.

    [:octicons-arrow-right-24: Quick start](getting-started.md)

-   :material-play-circle-outline:{ .lg .middle } **See the full product**

    ---

    Deploy the demo stack with Dex SSO, PostgreSQL, Gitea, MinIO backups, and a
    verified external-auth flow.

    [:octicons-arrow-right-24: Full demo](demo.md)

-   :material-server-network:{ .lg .middle } **Prepare production**

    ---

    TLS, PostgreSQL, OIDC, stable keys, private registries, HA, monitoring, and
    backup runbooks.

    [:octicons-arrow-right-24: Deployment guide](deploy.md)

</div>

## How it works

```text
Browser ──────────► Portal (:8080) ──────────► Kubernetes API
                                               (workspaces, pods, PVCs)

VS Code / Cursor / PyCharm / ssh ──► Bastion (:2222) ──► workspace agent ──► sshd (:22)
```

The **portal** serves the UI/API and manages workspace lifecycle. The **SSH
bastion** provides a stable entry point without exposing workspace pods. A
**workspace** is a StatefulSet with a persistent home, a workspace agent, and
optional IDE sidecars. **PostgreSQL** stores the control plane: users, RBAC,
quotas, access grants, encrypted tokens and secrets, share links, and audit
events. **Kubernetes** remains the runtime source of truth for pods, StatefulSets,
and PVCs.

[Read the product tour](guided-tour.md){ .md-button }
[Architecture details](architecture.md){ .md-button }

## When poddy is a good fit

poddy is designed for small and medium engineering teams that already run
Kubernetes and want a self-service development layer without building one from
scratch.

**Great fit**

- Platform teams standardising development environments across several teams.
- Teams that need persistent cloud workspaces but want to keep using local IDEs.
- Organisations that require SSO, ownership, quotas, audit, and private images.
- Teams that want to start on k3s and grow to EKS, GKE, AKS, or multi-region.

**Not the right tool**

- A single developer who only needs a local container. Use Dev Containers or a
  local VM.
- A fully managed SaaS with no Kubernetes operations at all.
- Hard multi-tenant isolation between untrusted customers. Use separate
  clusters or namespaces and additional admission controls.

## Documentation map

| I want to… | Start here |
|------------|------------|
| Install quickly | [Quick start](getting-started.md) |
| Understand the moving parts | [Product tour](guided-tour.md) |
| Use workspaces every day | [Workspaces](workspaces.md) |
| Operate poddy | [Operations](operations.md) |
| Add my own image | [Templates](templates.md) |
| Connect GitHub or GitLab | [External authentication](external-auth.md) |
| Configure SSO | [OIDC](oidc.md) / [Okta](okta.md) |
| Control access | [RBAC](rbac.md) |
| Fix a problem | [Troubleshooting](troubleshooting.md) |
