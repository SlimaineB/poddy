# Product tour

poddy gives developers a real development machine on Kubernetes without making
them learn Kubernetes. This page explains the mental model: what runs where,
what users see, and what the platform team controls.

## The problem poddy solves

A development workspace needs more than a container:

- a home directory that survives restarts;
- SSH or an IDE connection;
- credentials for private Git repositories;
- CPU, memory, and storage limits;
- a way to stop idle environments;
- access rules, audit, and quotas;
- a repeatable image and configuration.

Doing this by hand for every developer means maintaining kubeconfigs, SSH
configs, namespaces, Secrets, PVCs, and a wiki that is always slightly out of
date. poddy turns that into one portal and one template catalogue.

## The three planes

| Plane | Components | Responsibility |
|-------|------------|----------------|
| **Control plane** | Portal + PostgreSQL | Sign-in, RBAC, quotas, templates, workspace lifecycle, encrypted secrets, audit, and the UI/API. |
| **Access plane** | SSH bastion + workspace agent | Stable SSH entry point, owner checks, port forwarding, terminal, and IDE proxying. |
| **Runtime plane** | Workspace StatefulSet + Pod + PVC | The developer's Linux environment, home directory, tools, and sidecars. |

```text
Browser ──────────► Portal ──────────► Kubernetes API
                       │                 (StatefulSets, Pods, PVCs)
                       │
                       └── PostgreSQL (control plane)

VS Code / Cursor / PyCharm / ssh ──► Bastion ──► workspace agent ──► sshd (:22)
```

The portal and bastion are built from the same image but run as separate
Deployments. That lets HTTP/API traffic and SSH traffic scale independently.

## A day in the life

1. **Sign in.** The user opens the portal. With OIDC, they use their company
   identity. With a shared token, they use one operator token.
2. **Pick a template.** The template defines the image, resources, storage,
   sidecars, ports, links, and any required external accounts.
3. **Create a workspace.** The portal renders the template, checks access and
   quotas, creates the StatefulSet and PVC, and records the owner.
4. **Wait for Running.** The workspace starts `sshd`, the poddy agent, and any
   IDE sidecars. Startup logs are available in the workspace details dialog.
5. **Work.** The user opens the browser terminal, VS Code Web, Remote-SSH,
   JetBrains Gateway, JupyterLab, or a previewed port.
6. **Stop when idle.** The pod scales to zero, the PVC stays. Start it later and
   the home directory is still there.
7. **Delete when done.** The workspace and its runtime objects are removed. The
   volume can be backed up first if configured.

## Concepts

| Concept | Meaning |
|---------|---------|
| **Workspace** | A developer environment backed by a StatefulSet, a Pod, and optionally a PVC. Owned by the user who created it. |
| **Template** | A YAML definition of the workspace image, parameters, pod overlay, links, and external-auth requirements. |
| **Region** | A Kubernetes cluster managed by a region manager. The central hub routes workspace operations through outbound tunnels. |
| **Provider** | An OAuth2 provider configured for workspace credentials, such as GitHub, GitLab, Gitea, or Azure DevOps. |
| **Connection** | A user's stored token for one provider. Workspaces can require a connection before they start. |
| **Team** | A named group of identities that can be assigned to templates and carry quota defaults. |
| **Quota** | A per-user cap on workspaces, CPU, memory, and storage. Team quotas are defaults; user overrides win field by field. |
| **Share link** | A signed link that exposes a workspace port internally or publicly. |
| **Audit event** | An append-only record of a sensitive action, such as workspace creation, terminal use, or access changes. |

## What the platform team controls

- **Images.** Use the published base, Go, Node.js, and Python images or build
  your own from `poddy-workspace-base`.
- **Templates.** Define what a workspace looks like without changing portal code.
- **Identity.** OIDC/SSO groups and users, or a shared token for a deliberate
  single-operator deployment.
- **Access.** Global admins, auditors, template owners, teams, and per-template
  users/teams.
- **Quotas.** Workspaces, CPU, memory, and storage per user, with team defaults.
- **Storage.** StorageClass, ephemeral vs persistent homes, and S3 backups.
- **Network.** Ingress, TLS, LoadBalancer or NodePort SSH, registry mirrors, and
  port sharing policy.
- **Observability.** Prometheus metrics, logs, and the PostgreSQL audit trail.

## What users control

- Which template to use and the allowed parameters.
- Their workspace lifecycle: stop, start, restart, delete.
- Their SSH keys and connected external accounts.
- Their own environment variables, workspace values, and share links.
- Their workspace ports and previews.

## Design principles

### Kubernetes is the runtime source of truth

Workspaces are StatefulSets, Pods, and PVCs. poddy does not hide them behind a
proprietary runtime. Platform teams can inspect, back up, and govern them with
normal Kubernetes tools.

### PostgreSQL is the control-plane source of truth

Users, RBAC, quotas, access grants, encrypted tokens and secrets, shares, and
audit events live in PostgreSQL. Kubernetes ConfigMaps are mirrors and migration
inputs, not the authority.

### Credentials stay short-lived

Workspaces receive short-lived workspace tokens and refreshed external-auth
tokens. Users do not receive cluster credentials. SSH is owner-scoped and the
bastion re-checks ownership on every connection.

### Templates are explicit, not magic

A template is a small YAML overlay. The portal fills in safe defaults for SSH,
the home volume, security context, and the workspace container. Anything the
template declares wins, and the merge rules are documented.

### No hidden SaaS dependency

poddy is MIT licensed and runs in your cluster. The only external services are
the ones you choose: your identity provider, PostgreSQL, an S3 bucket, and your
image registry.

## What poddy deliberately does not do

- It is not a multi-tenant SaaS control plane for untrusted customers. Use
  separate clusters or namespaces plus admission policies for hard isolation.
- It is not a Kubernetes abstraction that hides the workload objects. Workspaces
  remain inspectable StatefulSets and PVCs.
- It does not replace your image registry, identity provider, database, or
  object storage.
- It does not automatically fail over a running workspace between regions.

## Next steps

- [Quick start](getting-started.md): install it and create a workspace.
- [Workspaces](workspaces.md): the day-to-day user guide.
- [Templates](templates.md): define your own workspace types.
- [Architecture](architecture.md): request flows and internals.
- [Operations](operations.md): run it after the first install.
