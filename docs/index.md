# poddy

<div class="hero" markdown>

**Kubernetes development workspaces without the Kubernetes homework.**
Create a workspace, open a browser terminal, or connect VS Code over SSH. Users
never need a kubeconfig.

[Get started locally](getting-started.md){ .md-button .md-button--primary }
[Deploy with Helm](deploy.md){ .md-button }
[View on GitHub](https://github.com/SlimaineB/poddy){ .md-button }

</div>

<div class="grid cards" markdown>

-   :material-rocket-launch:{ .lg .middle } **Getting started**

    ---

    Go from zero to a running workspace with Helm and published images.

    [:octicons-arrow-right-24: Quick start](getting-started.md)

-   :material-kubernetes:{ .lg .middle } **Deploy**

    ---

    Production Helm charts, TLS, SSH, scaling, and secrets.

    [:octicons-arrow-right-24: Kubernetes deployment](deploy.md)

-   :material-puzzle:{ .lg .middle } **Workspace templates**

    ---

    Define images, parameters, and actions in plain YAML.

    [:octicons-arrow-right-24: Template reference](templates.md)

-   :material-shield-lock:{ .lg .middle } **Access control**

    ---

    OIDC, shared tokens, teams, and owner-scoped SSH.

    [:octicons-arrow-right-24: OIDC and RBAC](oidc.md)

-   :material-sitemap:{ .lg .middle } **Architecture**

    ---

    Request flows, components, and why Kubernetes stays the source of truth.

    [:octicons-arrow-right-24: Architecture](architecture.md)

</div>

## What you get

- **One control plane.** A browser portal to create, stop, start, restart, and
  delete workspaces.
- **Scale what you need.** The portal and SSH bastion run as separate
  Deployments, so HTTP/API and SSH capacity scale independently.
- **Four ways to work.** Browser terminal, browser VS Code (opt-in), SSH
  through a bastion, or VS Code Remote-SSH.
- **Real dev containers.** Persistent volumes, custom images, and templates that
  declare CPU, memory, storage, and links.
- **Team-ready access.** OIDC / SSO, global admins, template owners, teams, and
  per-template access.
- **No database required.** Kubernetes ConfigMaps and Secrets are the source of
  truth. An optional PostgreSQL mirror adds metadata durability, an audit log,
  and disaster-recovery snapshots without becoming the source of truth for
  workspace or RBAC decisions.
- **No cluster credentials for users.** The portal talks to the Kubernetes API;
  users only need a browser or an SSH key.

## How it works

```text
Browser ─────────► Portal (:8080) ─────────► Kubernetes API
                                             (workspace lifecycle)

VS Code / ssh ───► Bastion (:2222) ────────► Kubernetes API
                                             (pods/portforward)
                           │
                           └── ProxyJump / port-forward ──► Workspace pod :22
```

The portal and bastion are separate Deployments built from the same image. The
portal is stateless and scales behind a LoadBalancer; the bastion shares a host
key across replicas. See [Architecture](architecture.md) for the full request
flow and [Data model](data-model.md) for the Kubernetes objects involved.

## Common paths

| I want to… | Start here |
|------------|------------|
| Try it locally in a few minutes | [Getting started](getting-started.md) |
| Run it in production | [Kubernetes deployment](deploy.md) |
| Run it on Amazon EKS with ALB and EFS | [Amazon EKS](eks.md) |
| Add my own workspace image | [Workspace templates](templates.md) |
| Build poddy from source | [Development](development.md) |
| Enable PostgreSQL metadata, audit, and backups | [PostgreSQL schema](postgres-schema.md) |
| Connect GitHub or GitLab inside a workspace | [External authentication](external-auth.md) |
| Configure SSO | [OIDC / SSO](oidc.md) |
| Configure Okta SSO | [Okta SSO](okta.md) |
| Understand who can see what | [RBAC and ownership](rbac.md) |
| Understand the request flow and components | [Architecture](architecture.md) |
| Inspect the Kubernetes and PostgreSQL objects | [Data model](data-model.md) |
