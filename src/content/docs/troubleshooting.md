---
title: "Troubleshooting"
description: "Diagnose installation, sign-in, template, runtime, access, backup, and multi-region failures."
---

Start with the layer that is failing: installation, sign-in, templates,
workspace runtime, access, external auth, backups, or multi-region. Most failures
are visible in one of these commands:

```bash
kubectl -n poddy-system get pods,deploy,svc
kubectl -n poddy-system logs deploy/poddy --tail=200
kubectl -n poddy-system logs deploy/poddy-bastion --tail=200
kubectl -n poddy-workspaces get statefulsets,pods,pvc
kubectl get events -A --sort-by=.lastTimestamp | tail -40
```

Use `kubectl describe` on the failing object before restarting anything.

## Installation and startup

### The portal Pod is `CrashLoopBackOff`

```bash
kubectl -n poddy-system logs deploy/poddy --previous --tail=200
kubectl -n poddy-system describe pod -l app.kubernetes.io/name=poddy
```

Common causes:

| Log or event | Fix |
|--------------|-----|
| `database URL is required` / connection refused | Create `poddy-database` and check the host, port, database, user, password, and `sslmode`. |
| `database connect timeout` | PostgreSQL is not ready or not reachable from the pod. Check the Service, NetworkPolicy, and DNS. |
| `migration failed` | Check database permissions and schema ownership; run with `database.autoMigrate=false` only if you manage migrations yourself. |
| `no such file` for a Secret key | The Secret exists but the expected key is missing (`token`, `database-url`, etc.). |
| `exec format error` | The image tag does not match the node architecture. |
| `invalid OIDC issuer` | The issuer is unreachable from the portal or has a trailing path mismatch. |

The portal retries the database for `database.connectTimeout` (default `30s`)
and exits if it is still unavailable.

### The portal is running but returns 502/503 through Ingress

- Confirm `kubectl -n poddy-system get endpointslice` shows ready portal
  endpoints.
- Check the Service target port and the Ingress backend.
- Increase proxy timeouts for terminal, IDE, and WebSocket routes.
- Ensure the Ingress forwards WebSocket upgrades for terminal, port previews,
  and `/api/internal/region/tunnel`.
- Check TLS certificate and host rules.

### The bastion Pod is crashing

- `ssh.enabled` or `bastion.enabled` is inconsistent.
- The host key Secret is missing or unreadable.
- The SSH CA Secret is missing when certificates are enabled.
- Port `2222` is already bound inside the container.

Inspect:

```bash
kubectl -n poddy-system logs deploy/poddy-bastion --previous
kubectl -n poddy-system get secret | grep -E 'ssh|ca|host'
```

### Pods are `Pending` because of resources

```bash
kubectl -n poddy-system describe pod -l app.kubernetes.io/name=poddy | tail -40
kubectl describe nodes | grep -A5 'Allocated resources'
```

Reduce requests, add nodes, or check taints and affinity. Workspace Pods can
also be pending for the same reasons.

## Sign-in

### The shared token is rejected

Read the actual token; do not assume the Secret value:

```bash
kubectl -n poddy-system get secret poddy-auth -o jsonpath='{.data.token}' | base64 -d
```

Check that `auth.existingSecret` and `auth.secretKey` point at the right Secret
and key. After changing the Secret, restart the portal.

### OIDC redirects back with an error

- The redirect URI must match exactly:
  `https://<poddy-host>/oidc/callback`.
- The issuer must match the provider's discovery document, including any path.
- The client ID and secret must belong to the same application.
- The browser and cluster clocks must be synchronized.
- `allowedGroups` or `allowedUsers` may be rejecting the account.
- With a private CA, set `hostAliases` or mount the CA so the portal can reach
  the issuer.

Check:

```bash
kubectl -n poddy-system logs deploy/poddy --tail=200 | grep -i oidc
kubectl -n poddy-system get secret poddy-oidc -o jsonpath='{.data.oidc-client-id}' | base64 -d
```

When the session key changes, all existing sessions are invalidated. That is
expected after rotation.

### Login succeeds but the user sees no workspaces

- The user is signed in as a different identity than the workspace owner.
- The user is not an admin and does not own the workspace.
- RBAC hides templates, not existing owned workspaces, but the region filter may
  hide another region.
- Check the owner annotation:

```bash
kubectl -n poddy-workspaces get statefulset <workspace> \
  -o jsonpath='{.spec.template.metadata.annotations.poddy\.io/owner}'
```

## Templates

### No templates and **New workspace** has no choices

```bash
kubectl -n poddy-workspaces get configmap -l poddy.io/template=true
kubectl -n poddy-system logs deploy/poddy --tail=200 | grep -i template
```

- Install `poddy-templates` in `workspaceNamespace`, not in `poddy-system`.
- The ConfigMap must have `poddy.io/template=true`, `template.yaml`, and
  `pod.yaml`.
- `poddy.io/template-name` or the ConfigMap name must match `name` in
  `template.yaml`.
- The portal watches the workspace namespace; a template in another namespace is
  invisible.

### A template update was rejected

Template validation is strict. Look for the error in the portal logs and check:

- YAML syntax and duplicate keys;
- required `name` and `image`;
- parameter types, defaults, and duplicate option values;
- link URLs and duplicate link names;
- reserved parameter names `name` and `template`;
- `pod.yaml` rendering errors.

The metric `poddy_template_reloads_total{result="error"}` increments on failed
reloads. Fix the ConfigMap; the portal retries the watch event.

### A workspace does not pick up a template change

A stopped workspace keeps its preserved pod template. A running workspace also
keeps its current Pod. Delete and recreate the workspace, or stop/start after the
template change, to adopt new template defaults.

## Workspace runtime

### Workspace stays `Pending`

```bash
kubectl -n poddy-workspaces describe pod poddy-<workspace>-0
kubectl -n poddy-workspaces get pvc
kubectl get storageclass
```

Check:

- no schedulable node for the requested CPU/memory;
- no default StorageClass and no `workspace.storageClass`;
- PVC stuck in `Pending` because the provisioner cannot create it;
- node selectors, taints, or affinity in the template;
- quota or LimitRange denial.

### `ImagePullBackOff` or `ErrImagePull`

```bash
kubectl -n poddy-workspaces describe pod poddy-<workspace>-0 | grep -A10 Events
```

- The image or tag does not exist.
- The cluster cannot reach the registry.
- A private registry needs `workspace.imagePullSecrets` in the workspace
  namespace.
- A registry mirror is configured incorrectly in `workspace.imageRegistry` or
  `workspace.imageRegistryMap`.
- A custom template overrides only part of the image reference.

The agent sidecar uses the portal image by default. If that image is private, add
its pull Secret to `workspace.imagePullSecrets` too.

### Workspace is `CrashLoopBackOff`

```bash
kubectl -n poddy-workspaces logs poddy-<workspace>-0 -c workspace --previous
kubectl -n poddy-workspaces logs poddy-<workspace>-0 -c poddy-agent --previous
```

- A custom entrypoint does not start `sshd` or `/usr/local/bin/poddy-ssh-start`.
- The home volume has wrong permissions.
- The image lacks `bash`, `sshd`, or required tools.
- A sidecar or init container fails first; check all containers.
- The template overrides the command and removes the SSH startup.

### Workspace is stuck `Terminating`

```bash
kubectl -n poddy-workspaces describe statefulset poddy-<workspace>
kubectl -n poddy-workspaces describe pod poddy-<workspace>-0
kubectl -n poddy-workspaces get pvc poddy-<workspace>
```

The portal deletes with zero grace and waits for the Pod. A finalizer, an
unresponsive node, or a stuck volume attach can block it. Check node health and
the PVC finalizers. Do not delete the PVC before deciding whether the data is
needed.

### Stop or start returns an error

- The workspace must exist; a stale card may refer to a deleted object.
- Quota may block start after a quota reduction.
- The Kubernetes API may be throttling. Check `kubernetes.qps` and
  `kubernetes.burst`.
- A StatefulSet update conflict is normally retried. If it persists, check the
  portal logs and the StatefulSet generation.

### Ephemeral workspace lost files

`storage: ""` uses an `emptyDir`. Files are lost whenever the Pod is recreated,
including stop/start. Recreate the workspace with a persistent storage size.

## SSH and desktop IDEs

### Connection refused

- The bastion is not running: `kubectl -n poddy-system get deploy poddy-bastion`.
- `ssh.publicHost`/`ssh.publicPort` do not match the Service or LoadBalancer.
- In a local install, start the port-forward:
  `kubectl -n poddy-system port-forward svc/poddy-ssh 2222:2222`.
- The Service selects no ready bastion Pods.

### Connection times out

- Firewall or security group blocks the SSH port.
- LoadBalancer is still provisioning.
- The Ingress does not support TCP/WebSocket for the tunnel path.
- The workspace agent is not connected and `ssh.requireAgent=true`.

Check the agent sidecar:

```bash
kubectl -n poddy-workspaces logs poddy-<workspace>-0 -c poddy-agent --tail=100
kubectl -n poddy-system logs deploy/poddy-bastion --tail=100
```

### `Permission denied (publickey)`

- Add the public key in **SSH keys** and restart the workspace.
- The key is mapped to a different identity.
- The workspace predates the key; stop/start recreates `authorized_keys`.
- Use the exact SSH config from the **SSH** dialog, including `User dev`.
- For a certificate, check the CA Secret and certificate expiration.

### `Host key verification failed`

The bastion host key changed, usually after recreating the release without
`ssh.existingHostKeySecret`. Confirm the change with your team before removing
the old entry from `known_hosts`. For stable host keys, provide the Secret.

### SSH config works on Linux/macOS but not Windows

A config saved only in WSL is invisible to Windows VS Code, Cursor, and
Gateway. Save the generated config in `C:\Users\<you>\.ssh\config` and place the
private key in the same `.ssh` directory, or use the Poddy extension.

### JetBrains Gateway cannot connect

Gateway does not support `ProxyJump`. Use the generated block, which uses
`ProxyCommand`, and enable **Parse config file ~/.ssh/config** in Gateway.

### VS Code or Cursor does not open

- Install the Poddy VSIX once in that editor.
- Use a desktop editor, not a WSL window.
- The extension version in the portal must be available for download.
- The workspace must be **Running** and owned by the signed-in user.
- The SSH config block must not be overwritten by another tool.
- For local mode, the portal must be able to start the port-forward.
- Check the editor's Remote-SSH output for the failing command.

### VS Code reinstalls its server after every start

The workspace home is ephemeral or the PVC was recreated. Use persistent
storage; the server lives in `~/.vscode-server`.

## Browser terminal and web IDEs

### Terminal returns 403 `access_required`

The signed-in admin does not own the workspace. The owner must approve access in
the workspace **Access** panel. Owners always have access.

### Terminal opens but immediately disconnects

- The ingress proxy times out WebSocket connections.
- The Pod was restarted or evicted.
- `pods/exec` permission is missing from the portal Role.
- A NetworkPolicy blocks the portal-to-Pod path.

### VS Code Web is missing

The template must set `browserVSCode: "true"`. The built-in templates enable it
by default. A custom template that sets it to `false` omits the sidecar.

### JupyterLab is missing

The workspace needs the `jupyterlab` sidecar, enabled by the Python template's
`jupyterLab` parameter. The button opens port `8888` through the proxy.

### Port preview is blank or 404

- The server must listen on `0.0.0.0`, not only `127.0.0.1`.
- The port must appear in **Ports**; refresh after starting the server.
- The app may use absolute asset paths that break under a subpath. Use the
  share link path or configure the app's base path.
- WebSocket connections need upgrade support in the ingress.

## Shares and access

### A public share link still asks for login

The link is **Internal**, not **Public**, or an old private URL was copied.
Change the mode in **Ports** and copy the new link.

### A share link returns 404

- The workspace was deleted, which removes its share links.
- The port was set back to **Private**.
- The token was truncated or copied without the region prefix.
- The link belongs to a region and the region is unreachable.

### An admin cannot open another user's workspace

Request owner approval from the workspace **Access** panel. Grants last 30
minutes. Check that the workspace has an owner annotation; legacy workspaces
without an owner remain open to admins.

## External authentication

### A workspace cannot be created because a provider is required

Connect the provider in **Connections** first. Required providers return HTTP
`409` until the user has a stored token. Optional providers do not block
creation.

### The OAuth callback fails

- The provider callback URL must be
  `https://<poddy-host>/external-auth/<id>/callback`.
- Client ID, client secret, scopes, and tenant settings must match.
- The `redirectBase` must be the browser-visible portal URL, not the in-cluster
  Service URL.
- For Azure DevOps, check the organization and tenant configuration.
- Check the portal logs for the provider error; do not log tokens.

### The workspace has no `GITEA_TOKEN`/`GITHUB_TOKEN`

- The template must declare `externalAuth` for that provider.
- The provider must be connected before the workspace starts.
- Stop/start or recreate the workspace after connecting.
- Check the workspace environment in the details dialog and the StatefulSet
  annotation `poddy.io/external-auth-env`.

### Token refresh fails

- The external-auth encryption key changed. Restore the original key or have
  users reconnect.
- The refresh token expired or was revoked at the provider.
- The workspace token expired. Restart the workspace.
- The provider token endpoint is unreachable from the cluster.

Inside the workspace:

```bash
poddy external-auth status gitea
poddy external-auth login gitea
poddy external-auth token gitea
```

## Backups and restores

### Backup returns 501

Backups are disabled or incomplete. Set `backup.enabled=true`, the S3 endpoint,
bucket, region, and credentials. Check the portal startup log for
`volume backups enabled`.

### Backup fails with 403

The S3 credentials cannot list/get/put/delete the configured prefix. Check the
bucket policy and the `prefix` value. For MinIO, verify the endpoint and
`useSSL`.

### Backup fails for a `ReadWriteOncePod` volume

Live backup is not supported. Stop the workspace first.

### Restore fails

- The workspace must be **Stopped**.
- The target volume must exist and be `Bound`.
- The backup key must belong to that volume.
- `clean: true` removes files not in the archive; make sure that is intended.
- Check MinIO/S3 connectivity and the helper Pod logs.

### A volume is unassigned

It is not attached to a workspace. Admins can inspect it on the **Volumes**
page. Confirm it is not needed before deleting it. A volume still assigned to a
workspace cannot be deleted; delete the workspace first.

## RBAC, quotas, and audit

### A user gets 403

- The template is restricted to other users or teams.
- The user is not the workspace owner and is not an admin.
- The admin has not been approved to enter someone else's workspace.
- The action requires a global admin, not a template owner.

### A user gets 404 instead of 403

poddy intentionally hides resources the user cannot see. Check the signed-in
identity, owner annotation, and region filter. A workspace in another region may
not be routable.

### Quota exceeded

```bash
kubectl -n poddy-system logs deploy/poddy --tail=200 | grep -i quota
```

Stop or delete unused workspaces, or raise the user/team quota. Stopped
workspaces still count because their volumes and metadata exist.

### Audit page is empty

- The user needs `rbac.auditors` or global admin.
- Audit events are stored in PostgreSQL, not Kubernetes.
- Check database connectivity and the audit retention policy.

### RBAC changes do not appear

PostgreSQL is authoritative and `poddy-rbac` is a mirror. Check that
`rbac.createConfigMap` is not overwriting a UI-managed document on every Helm
upgrade. Set it to `false` after the initial seed if admins manage access from
the UI.

## Performance

### The portal feels slow

- Raise `kubernetes.qps`/`kubernetes.burst` when many users open terminals or
  browser IDEs.
- Check `poddy_http_request_duration_seconds` and in-flight requests.
- Check PostgreSQL latency and connections.
- Check whether an ingress is buffering WebSocket traffic.
- Check browser console and network timing; large workspace lists and topology
  views cost more than the basic list.

### Workspace creation is slow

- Image pulls dominate first creation. Pre-pull images or use a mirror.
- Check `poddy_workspace_create_duration_seconds`.
- Check scheduling pressure and PVC provisioning.
- Check Kubernetes API throttling in the portal logs.

### Terminals disconnect under load

- Increase ingress/proxy read timeouts.
- Raise the bastion replica count and resources.
- Check node memory pressure and OOM kills of `sshd` or the agent.
- Keep `ssh.agent.sidecar=true` so the agent has its own memory limit.

## Multi-region

### A region shows as disconnected

- Check the region manager logs and its outbound WebSocket to the hub.
- The hub Ingress must forward `/api/internal/region/tunnel` upgrades.
- The region key must match `poddy-regions` on the hub.
- Keep the hub at one replica or use sticky routing.
- Check firewall egress from the region to the hub.

### Workspace requests return 502

The hub cannot reach the region. Region list and topology may return partial
data; workspace-specific requests fail until the tunnel reconnects. Check the
region Deployment and the hub logs.

### SSH works in one region but not another

- Every region bastion needs the same SSH CA Secret as the hub.
- The region's `ssh.publicHost` and Service must be reachable.
- The workspace agent must connect to its own region bastion.
- Restart workspaces created before a CA or agent change.

## Collect a debug bundle

```bash
kubectl -n poddy-system get pods,deploy,svc,ingress -o wide
kubectl -n poddy-system logs deploy/poddy --tail=500
kubectl -n poddy-system logs deploy/poddy-bastion --tail=500
kubectl -n poddy-workspaces get statefulsets,pods,pvc,configmap
kubectl get events -A --sort-by=.lastTimestamp | tail -100
```

Redact tokens, cookies, client secrets, database URLs, private keys, and
workspace file contents before sharing logs.

## Still stuck?

Include:

- poddy version (`poddy_build_info` or the image tag);
- Kubernetes distribution and version;
- chart version and relevant values with secrets removed;
- exact error text and timestamps;
- whether the failure affects one user, one workspace, one template, one region,
  or everyone.
