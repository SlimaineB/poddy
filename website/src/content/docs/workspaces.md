---
title: "Workspaces"
description: "Create, connect, stop, share, and remove Kubernetes development workspaces."
---

A workspace is a development machine backed by a Kubernetes StatefulSet, a
Pod, and usually a persistent home volume. This page is the day-to-day guide:
create, connect, stop, share, and remove workspaces.

## Find your workspaces

Sign in to the portal. The main view lists the workspaces you can access:

- **Your workspaces** are owned by your signed-in identity.
- **Admins** can see every workspace, but entering someone else's workspace
  requires owner approval. See [Workspace access](workspace-access.md).
- **Regions** appear in the top bar and on workspace cards in a multi-region
  deployment.
- **Search** filters by name, owner, template, or region.

Each card shows the phase, template, region, owner (for admins), connected users,
and any pending access request.

## Create a workspace

Click **New workspace** and choose:

| Field | Meaning |
|-------|---------|
| **Template** | Image, tools, parameters, ports, and links for the environment. |
| **Name** | Optional workspace suffix. The server prefixes it with your username, for example `alice-dev`. Leave it empty for a generated name. |
| **Region** | Where to create the workspace. Only shown when more than one region exists. |
| **Parameters** | Template-defined choices such as storage size or image tag. |
| **Resources** | CPU and memory requests/limits, bounded by your quota. |
| **Storage** | Persistent home size, or ephemeral storage with no PVC. |

The defaults come from the template and your quota. A workspace that requests
more than your remaining quota is rejected before Kubernetes objects are
created. Quotas count every workspace, including stopped ones, because the
volume and metadata still exist.

:::tip[Persistent vs ephemeral home]
Choose a persistent volume if you need files, installed packages, and IDE
server state to survive stop/start. With `storage: ""`, the home is an
`emptyDir` and is lost when the Pod is recreated. Persistent homes are the
right default for real development.
:::

### What happens after Create

1. The portal validates the template, RBAC, and quota.
2. It creates or updates the workspace StatefulSet and PVC.
3. The Pod starts `sshd`, the poddy agent sidecar, and any template sidecars.
4. The phase moves from **Pending** to **Running**.
5. The workspace card enables Terminal, IDE, SSH, Ports, and sharing actions.

The first start may take longer while the cluster pulls the image. Use
**Details → Logs** if it does not become **Running**.

## Workspace phases

| Phase | Meaning |
|-------|---------|
| **Pending** | The StatefulSet exists but the Pod is not scheduled or is starting. |
| **Running** | The Pod is running and the workspace is ready to use. |
| **Stopped** | Replicas are zero. The PVC remains. Start it again when needed. |
| **Terminating** | Deletion is in progress. |
| **Failed** | The Pod failed. Open logs and events in the details dialog. |
| **Unknown** | Kubernetes has not reported a usable phase yet. |

The details dialog shows the pod status, image, template, resources, node,
volumes, links, and recent logs. Admins also see manifests and can inspect the
raw Kubernetes objects.

## Connect

### Terminal

The **Terminal** button opens a browser terminal in `/home/dev`. It is the
fastest way to check the environment and install extra tools.

### VS Code Web

The **VS Code Web** button opens the browser editor through the portal proxy. It
requires the template parameter `browserVSCode: "true"`; the built-in templates
enable it by default. State lives in `/home/dev/.openvscode-server`.

### Desktop VS Code and Cursor

The **VS Code** and **Cursor** buttons use Remote-SSH through the Poddy
extension:

1. Install the Poddy VSIX once in that editor.
2. Click the portal button.
3. The extension enrolls a dedicated key, writes the SSH config with the
   bundled `poddy-connect` `ProxyCommand`, and opens the workspace. No
   Tailscale installation is required.

In a local deployment the portal can also write the SSH config and start a
port-forward for you. In cluster mode it returns a short-lived signed editor
URL. No manual `~/.ssh/config` edit is required. The **SSH** dialog is the
manual fallback.

The desktop editor server is stored in `~/.vscode-server` or the Cursor
equivalent. Use a persistent home volume if you want it to survive stop/start.

### PyCharm / JetBrains Gateway

Click **PyCharm**, or copy the **SSH** config and open it in JetBrains Gateway.
Gateway must parse `~/.ssh/config`; the generated block uses `ProxyCommand`
because Gateway does not support `ProxyJump`. Gateway then offers to install an
IDE backend in the workspace.

### JupyterLab

The **JupyterLab** button opens `/lab/` through the authenticated proxy. It is
shown when the workspace has the `jupyterlab` sidecar, which the Python template
enables by default.

### SSH

Add your public key in **SSH keys**, then open the **SSH** dialog and copy the
generated config. It covers every workspace you own:

```text
Host poddy-ssh-*
  HostName ssh.example.com
  Port 2222
  User dev
  ProxyCommand ...
```

For direct SSH in a local install, port-forward the portal and use the config
from the dialog:

```bash
kubectl -n poddy-system port-forward svc/poddy-ssh 2222:2222
ssh poddy-ssh-alice-dev
```

Production deployments usually expose the portal with a LoadBalancer or an SSH
TCP ingress instead. See [SSH access](ssh.md).

### Ports

The **Ports** dialog lists listening TCP ports detected in the workspace. Open a
port through the authenticated portal proxy, or create a share link.

| Mode | Who can open it |
|------|-----------------|
| **Private** | Only the owner and approved admins. No share link. |
| **Internal** | Any signed-in portal user. |
| **Public** | Anyone with the link, no portal login. |

Share links survive stop/start and are revoked when the port returns to
**Private** or the workspace is deleted. Treat a public link as a bearer
credential. See [Links and sharing](links.md).

## Stop, start, and restart

| Action | Effect |
|--------|--------|
| **Stop** | Scales the StatefulSet to zero. PVC and metadata remain. |
| **Start** | Scales back to one. The same home volume is mounted. |
| **Restart** | Deletes the Pod; the StatefulSet recreates it. |
| **Delete** | Removes the StatefulSet, Pod, PVC, SSH keys, environment secret, shares, and access state. |

Stopping frees CPU and memory. It does not delete the workspace or its volume.
Billing for the PVC storage continues.

:::caution[Delete is permanent]
Deleting a workspace also deletes its PVC. Back up the volume first if you
need the files. A PVC left behind by a failed or older deletion appears on
the [Volumes](volumes.md) page as unassigned.
:::

## Auto-stop schedules

Open a workspace's details and set an **Auto-stop** schedule:

- enable or disable the schedule;
- choose a timezone;
- choose a daily start time and stop time.

poddy stops the workspace at the stop time and starts it at the start time on
the next matching day. It only auto-starts a workspace that the schedule itself
stopped. A manual stop stays stopped, so a user's explicit choice is not
overridden.

The schedule is stored on the workspace and survives portal restarts. It applies
to one workspace at a time; use team quotas and templates for fleet-wide
defaults.

## SSH keys and external accounts

- **SSH keys** stores public keys used for SSH and workspaces. You can
  add, inspect, and remove keys.
- **Connections** stores OAuth tokens for providers such as GitHub, GitLab,
  Gitea, or Azure DevOps. Templates can require a connection before creation.
- Workspaces receive short-lived workspace tokens. They never receive your
  portal session or cluster credentials.

See [External authentication](external-auth.md) for provider setup and
[SSH access](ssh.md) for certificates and the agent path.

## Dotfiles

You can keep your shell, editor, and Git configuration in a dotfiles
repository. Open **Account → Dotfiles**, enter an HTTPS, SSH, or
`git@host:path` clone URL, choose a branch, and save. Poddy clones the
repository when a workspace starts and copies its top-level entries into
`/home/dev`. The branch defaults to `main`.

The copy skips `.git`, `.ssh`, and `authorized_keys`. Existing files are
overwritten on every workspace start, so edit and commit them in the repository
instead of editing only the workspace copy. Restart a running workspace to
apply a new repository or branch. Portal administrators can set a fallback
repository with `dotfiles.url` and `dotfiles.branch` in the Helm chart, or with
`PODDY_DOTFILES_URL` and `PODDY_DOTFILES_BRANCH`; a personal setting takes
precedence. Clear your setting to return to the fallback.

Only use repositories you trust: their files become part of your home
environment. A failed clone is logged to `/tmp/poddy-dotfiles.log` and does not
block SSH access. Private repositories can use the workspace's existing
[external-auth](external-auth.md) Git credentials when the template requires
that provider.

## Environment variables and secrets

Workspace **environment** values are managed from the details dialog. Use them
for non-secret configuration. For secrets:

- store them in a Kubernetes Secret and reference it with `envFrom` or
  `valueFrom` in the template;
- use external-auth connections for provider tokens;
- avoid putting secrets in template parameters, which are visible to users who
  can read the template.

See [Environment](environment.md) for precedence and merge behavior.

## Volumes and backups

The **Volumes** page lists PVCs in the workspace namespace. Admins can back up a
volume to S3, restore it into a stopped workspace, and delete orphaned volumes.
Backups are configured with `backup.enabled` and the `backup.s3` values. See
[Volumes and backups](volumes.md).

## Sharing and owner approval

Admins can inspect, stop, start, and delete any workspace. Entering a workspace
they do not own requires owner approval:

1. The admin clicks **Request access**.
2. The owner approves or denies it in the workspace **Access** panel.
3. The grant lasts 30 minutes and can be revoked at any time.

Presence shows who is currently connected. It is advisory, not authorization.
See [Workspace access](workspace-access.md).

## Teams, RBAC, and quotas

- **Templates** can grant access to individual users, teams, or everyone.
- **Teams** group identities and provide quota defaults.
- **User quotas** override team defaults field by field.
- **Global admins** manage users, regions, providers, templates, and all
  workspaces.
- **Auditors** can read the audit trail without managing workspaces.

See [RBAC and ownership](rbac.md) and [Architecture](architecture.md) for the
authorization model.

## CLI access

The `poddy` CLI can list workspaces and manage external-auth connections, which
is useful for headless setup and CI:

```bash
poddy auth login --portal https://poddy.example.com
poddy auth status
poddy auth connect github
poddy auth disconnect github
```

Run `poddy auth --help` for the flags supported by your build. The CLI uses the
same portal API and stored session as the browser.

## Troubleshooting

| Symptom | First check |
|---------|-------------|
| Workspace stays **Pending** | `kubectl -n poddy-workspaces describe pod <name>`; look for scheduling, PVC, or image-pull errors. |
| **ImagePullBackOff** | Registry credentials and `workspace.imagePullSecrets`; confirm the image tag exists. |
| **CrashLoopBackOff** | Workspace logs. A custom template may override the entrypoint or fail to start `sshd`. |
| Terminal connects but commands fail | Check the home volume permissions and template security context. |
| SSH asks for a password | The key is not in **SSH keys**, or the workspace was not restarted after the key was added. |
| VS Code keeps reinstalling | The home volume is ephemeral or the PVC was recreated. Use persistent storage. |
| Ports list is empty | Start a server inside the workspace and refresh. Only listening TCP ports appear. |
| Quota exceeded | Stop or delete unused workspaces; ask an admin to raise your quota. |

See [Troubleshooting](troubleshooting.md) for cluster-level failures.
