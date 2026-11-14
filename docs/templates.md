# Creating templates and images

## How templates work

A **template** is a pair of files:

```
my-template/
├── template.yaml   # metadata, parameters, default image
└── pod.yaml        # Go template → partial Pod overlay
```

Templates are not embedded in the portal. The portal loads them from these sources (later sources override earlier ones with the same name):

1. **Local directory** given by `-templates-dir` (or `PODDY_TEMPLATES_DIR`). It accepts either subdirectories (`node/template.yaml`, `node/pod.yaml`) or flat files (`node.template.yaml`, `node.pod.yaml`). The portal watches the directory and reloads on change.
2. **Kubernetes ConfigMaps** labeled `poddy.io/template=true` in the workspace namespace. Each ConfigMap contains `template.yaml` and `pod.yaml`. The portal watches them and reloads in place, so templates can be pushed at any time without restarting the portal.

The **default** `base`, `go`, `node` and `python` templates ship in the separate `poddy-templates` Helm chart, which deploys them as labeled ConfigMaps. The portal starts with an empty catalog and cannot create workspaces until templates are installed.

For ConfigMaps, the template name comes from the `poddy.io/template-name` label, or from the ConfigMap name when the label is absent. It must match `name` in `template.yaml`.

ConfigMaps can also carry `poddy.io/template-version` and `poddy.io/template-active=true`. This lets several versions of the same template coexist; the portal serves the active one. See [Versioning and rollback](#versioning-and-rollback).

## Security and trust

A template's `pod.yaml` is merged into the final Pod, so whoever can write a
template can control the pod spec: privileged containers, hostPath volumes, host
networking, service account tokens, and so on. Treat template ConfigMaps as
cluster-privileged objects.

- Only trusted operators and CI pipelines should have write access to the
  template ConfigMaps (Kubernetes RBAC). Template `owners` in poddy RBAC manage
  who can use a template, not its content.
- A per-team GitOps repository that renders templates needs deploy credentials:
  protect it like any other path to cluster-admin.
- Use admission policies (Kyverno, OPA Gatekeeper, Pod Security Admission) to
  reject privileged pods, hostPath volumes, or root users when teams do not need
  them. poddy does not vet pod fields.
- Poddy merges the overlay over its defaults; the defaults are not a security
  boundary.

## template.yaml schema

```yaml
name: node                    # required; must match directory/key/ConfigMap name
displayName: Node.js          # optional; defaults to name
description: Node workspace   # optional
image: ghcr.io/org/poddy-workspace-node:latest  # required
version: "1.2.0"              # optional; ConfigMap label overrides it
parameters:                   # optional list
  - name: cpu
    displayName: CPU
    type: string              # string (default), number, or boolean/bool
    description: CPU request
    default: "1"
    required: false
    options:                  # optional; renders a dropdown instead of a free field
      - label: 1 CPU
        value: "1"
      - label: 2 CPUs
        value: "2"
  - name: memory
    description: Memory request
    default: 2Gi
    options:
      - label: 2 GiB
        value: 2Gi
      - label: 4 GiB
        value: 4Gi
  - name: storage
    description: PVC size
    default: 10Gi
    options:
      - label: Ephemeral (no persistence)
        value: ""
      - label: 10 GiB
        value: 10Gi
  - name: storageClass
    description: StorageClass name (empty = default)
    default: ""
  - name: browserVSCode
    description: Run VS Code in the browser
    type: boolean
    default: "true"
  - name: jupyterLab
    description: Run JupyterLab on port 8888
    type: boolean
    default: "false"
links:                        # optional list; rendered on workspace cards
  - name: Cursor
    url: "cursor://vscode-remote/ssh-remote+{alias}/home/dev"
    icon: cursor
  - name: PyCharm
    url: "jetbrains-gateway://connect#type=ssh&host={alias}&port=22&user=dev&projectPath=/home/dev"
    icon: pycharm
  - name: JupyterLab
    url: "/api/workspaces/{name}/lab/"
    icon: jupyterlab
    newTab: true
  - name: Docs
    url: "https://docs.example.com/{name}"
    icon: external
    newTab: true
externalAuth:                 # optional; connect external OAuth2 accounts
  - id: github                # provider ID configured on the portal
    env: [GITHUB_TOKEN, GH_TOKEN]  # optional override
  - id: gitlab
    optional: true            # workspace can start without a connection
```

**Reserved parameter names**: `name`, `template`. Using them will fail validation.

All parameter values are strings. The portal passes them to the pod template as `.Parameters.<name>`. If a parameter has `required: true` and the user doesn't provide a value, creation fails.

`type` accepts `string` (default), `number`, or `boolean` (alias `bool`). The portal validates `number` and `boolean` values. When `options` is set, the creation form renders a dropdown; each option needs a `label` and a `value`, values must be unique, and `default` must match one of them.

`links` are validated with the template and versioned with it. See [links.md](links.md) for placeholders, icons, and security rules.

## External auth

Templates can require external OAuth2 accounts (GitHub, GitLab, Gitea, Azure DevOps, Bitbucket, or generic OIDC) with `externalAuth`:

```yaml
externalAuth:
  - id: github
    env: [GITHUB_TOKEN, GH_TOKEN]  # optional override of provider defaults
  - id: gitlab
    optional: true
```

- `id` must match a provider configured on the portal.
- Required providers block workspace creation/start with HTTP `409` until the user connects them in **Connections**.
- Optional providers are skipped when not connected.
- The portal injects the access token under the provider's default env names, plus `PODDY_EXTERNAL_AUTH_<ID>_TOKEN`.
- It also sets `PODDY_PORTAL_URL`, `PODDY_WORKSPACE_NAME`, `PODDY_WORKSPACE_TOKEN`, `PODDY_EXTERNAL_AUTH_IDS`, `GIT_ASKPASS`, and `GIT_TERMINAL_PROMPT=0`.
- Use `poddy-external-auth token <id>` in the workspace for a refreshed token. Git uses `poddy-git-askpass` automatically.

See [external-auth.md](external-auth.md) for provider configuration, Helm values, API endpoints, and troubleshooting.

## pod.yaml contract

`pod.yaml` is a Go `text/template` rendered into a partial `corev1.Pod`. Poddy then merges that partial pod over an internal default pod. The template is therefore a customization overlay, not a full pod definition.

The portal calls `template.Render(Data)` where `Data` contains:

| Field | Type | Description |
|-------|------|-------------|
| `.Name` | string | Workspace name (user-provided) |
| `.Namespace` | string | Workspace namespace |
| `.PodName` | string | `poddy-<name>-0` |
| `.PVCName` | string | `poddy-<name>` (PVC name if created) |
| `.Image` | string | Template image (or request override) |
| `.Parameters` | map[string]string | Resolved parameters (defaults + user input) |
| `.Storage` | string | Same as `.Parameters["storage"]` |
| `.AuthorizedKeys` | string | Newline-separated SSH public keys for the workspace owner plus admin keys (never the full global key set) |
| `.SSHCommand` | string | `/usr/local/bin/poddy-ssh-start` |
| `.WorkspacePath` | string | `/home/dev` |
| `.Param` | method | `Data.Param("cpu")` — same as `.Parameters.cpu` |
| `quote` | func | `strconv.Quote` — use for env values, paths |

**Defaults injected when missing:**
- The `workspace` container with the template image, `IfNotPresent`, SSH command, `SSH_AUTHORIZED_KEY`, working directory `/home/dev`, port 22, CPU/memory requests from the `cpu`/`memory` parameters, and the workspace mount.
- `automountServiceAccountToken: false` and pod `securityContext.fsGroup: 1000`.
- The `workspace` volume: PVC when `storage` is non-empty, `emptyDir` otherwise.
- The `code-server` sidecar when `browserVSCode` is `"true"`; it opens `/home/dev`.
- The portal sets `metadata.name`, `metadata.namespace`, labels (`app.kubernetes.io/name=poddy`, `poddy.io/workspace=<name>`, `poddy.io/template=<template>`), and `restartPolicy: Always` when empty.
- External auth env vars are added after rendering and recorded in the `poddy.io/external-auth-env` annotation so tokens are stripped from stopped StatefulSet templates.

**Customization and merge rules:**
- Any field present in `pod.yaml` wins over the default.
- Scalars replace the default value.
- Lists merge with the usual Kubernetes keys: containers by `name`, env by `name`, ports by `containerPort`, and volume mounts by `mountPath`.
- A volume defined in `pod.yaml` with the same name as a default volume replaces that default volume.
- Use the standard `$patch: replace` or `$patch: delete` directives when a list must be replaced or removed instead of merged.
- Templates are not limited to the default volume or environment. They can declare any number of additional PVCs, volumes, mounts, env vars, containers, and init containers; the defaults only fill what is missing.
- Define a `code-server` container to customize the IDE image, arguments, resources, or mounts. Set `browserVSCode: "false"` to omit the default sidecar.
- The final pod must still contain a container named `workspace`; the defaults provide one if the template does not.

**Example (minimal):**
```yaml
apiVersion: v1
kind: Pod
spec: {}
```

**Example (add environment without repeating defaults):**
```yaml
apiVersion: v1
kind: Pod
spec:
  containers:
    - name: workspace
      env:
        - name: GOPATH
          value: /home/dev/go
```

## Default templates

These are shipped by the `poddy-templates` chart, not by the portal. Install the
chart (or apply equivalent ConfigMaps) to use them.

| Name | Image | Parameters | Notes |
|------|-------|------------|-------|
| `base` | `ghcr.io/slimaineb/poddy-workspace-base:latest` | cpu, memory, storage, storageClass, browserVSCode | Debian 12, Git, curl, sudo, SSH |
| `go` | `ghcr.io/slimaineb/poddy-workspace:latest` | cpu, memory, storage, storageClass, browserVSCode | Go 1.25.5, `GOPATH=/home/dev/go`, larger defaults |
| `node` | `ghcr.io/slimaineb/poddy-workspace-node:latest` | cpu, memory, storage, storageClass, browserVSCode | Node.js 22, npm, pnpm, Yarn, TypeScript, tsx |
| `python` | `ghcr.io/slimaineb/poddy-workspace-python:latest` | cpu, memory, storage, storageClass, browserVSCode | Python 3.11, pip, venv, uv, build tools |

All four rely on the pod defaults for SSH, the `/home/dev` home volume, security
context, resources, and the `code-server` sidecar. Their `pod.yaml` only adds
the template-specific environment (`GOPATH`, `NODE_ENV`/`NPM_CONFIG_PREFIX`, or
`PYTHONUNBUFFERED`), which keeps them easy to read and override. Set
`browserVSCode: "false"` to disable the sidecar.

The images point at the upstream GHCR namespace; forks should override the
`image` field with their own published images.

## Adding a template

You do **not** need to clone the repository or rebuild the portal. The simplest
way is to push a labeled ConfigMap to the workspace namespace:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-template
  namespace: poddy-workspaces   # your workspaceNamespace
  labels:
    poddy.io/template: "true"
    poddy.io/template-name: my-template
    poddy.io/template-active: "true"
data:
  template.yaml: |
    name: my-template
    displayName: My template
    image: ghcr.io/my-org/my-workspace:latest
    parameters:
      - name: cpu
        default: "500m"
      - name: memory
        default: 1Gi
  pod.yaml: |
    apiVersion: v1
    kind: Pod
    spec: {}
```

```bash
kubectl -n poddy-workspaces apply -f my-template.yaml
```

The portal reloads within a few seconds and the template appears on the
**Templates** page. A template without an RBAC entry is public. To remove it:

```bash
kubectl -n poddy-workspaces delete configmap my-template
```

No portal restart or `helm upgrade` is required. If the workspace image is
private, add `imagePullSecrets` to the `pod.yaml` spec; the chart's top-level
`imagePullSecrets` only covers the portal and bastion pods.

## GitOps and one repository per team

Because templates are ordinary labeled ConfigMaps, a team can keep them in its
own repository and sync them with Argo CD or Flux into the workspace namespace.
The portal hot-reloads the catalog; no portal restart is needed.

- Keep one active ConfigMap per logical `poddy.io/template-name` in a namespace.
  Duplicates make the catalog ambiguous and the reload is rejected.
- Team releases of the `poddy-templates` chart should set
  `builtins.enabled=false` and use a distinct release name.
- Prefer Kustomize or a small Helm release when the team repository cannot run
  the main chart. Removing a template from Git deletes its ConfigMap and the
  portal drops it; existing workspaces keep their spec.
- See `deploy/helm/poddy-templates/README.md` for the multi-team rules and an
  example values file.

## Versioning and rollback

A template version is a labeled ConfigMap. To keep history, create one ConfigMap per version with the same `poddy.io/template-name` and a distinct `poddy.io/template-version`. Mark exactly one as active:

```yaml
metadata:
  name: node-2.0.0
  labels:
    poddy.io/template: "true"
    poddy.io/template-name: node
    poddy.io/template-version: "2.0.0"
    poddy.io/template-active: "true"
```

Rules:

- One active version per template. The portal rejects multiple active versions or multiple versions with none active, keeping the previous catalog.
- A single unversioned ConfigMap per template still works, so existing deployments keep working.
- The active version is shown in the Templates page; search also matches the version.
- Rollback does not need a portal restart.

Rollback with the Helm chart: keep both versions under `templates.<name>.versions`, then change `active`:

```bash
helm upgrade poddy-templates deploy/helm/poddy-templates -n poddy-workspaces \
  -f templates-values.yaml --set-string templates.node.active=1.0.0
```

Rollback with raw ConfigMaps: move the label to the old version:

```bash
kubectl -n poddy-workspaces label configmap node-1.0.0 poddy.io/template-active=true --overwrite
kubectl -n poddy-workspaces label configmap node-2.0.0 poddy.io/template-active-
```

The portal picks up the change through the watch. Old ConfigMaps can stay for history or be deleted once no longer needed. Workspaces already created keep their rendered spec; new workspaces use the active version.

### Operator checklist

1. Build and push the workspace image, or reuse a published image such as `ghcr.io/slimaineb/poddy-workspace-base:latest`.
2. Add the template to the separate `poddy-templates` release, or `kubectl apply` a labeled ConfigMap. Use `poddy.io/template-version` and `poddy.io/template-active` to keep versions for rollback.
3. Check the portal logs:
   ```bash
   kubectl -n poddy-system logs deploy/poddy --tail=100
   ```
4. Open **Templates** in the portal, search for the new name, and set access if needed. A template with no RBAC entry is public.

No portal restart or `helm upgrade` of the portal is required. If the workspace image is private, add `imagePullSecrets` inside the template's `pod` spec; the portal chart's top-level `imagePullSecrets` only applies to the portal and bastion pods.

**ConfigMap size limit**: 1 MiB per ConfigMap. Templates are small; split large templates into separate ConfigMaps or bake them into a custom portal image.

## Building custom workspace images

### Base image contract

`ghcr.io/slimaineb/poddy-workspace-base` provides:
- OpenSSH server on port 22
- `dev` user (UID 1000), passwordless sudo
- `/home/dev` home directory owned by `dev:dev`
- `/usr/local/bin/poddy-ssh-start` — writes `SSH_AUTHORIZED_KEY` to `/home/dev/.ssh/authorized_keys`, lowers `sshd`'s OOM score, installs the `/usr/local/bin/poddy-shell` wrapper, and starts `sshd`
- `CMD ["/usr/sbin/sshd", "-D", "-e"]` (overridden by pod `command`)

The pod defaults run `command: [{{ .SSHCommand | quote }}]`; a template can override it.
If you override the command, `sshd` no longer gets the OOM protection from
`poddy-ssh-start`.

The default Helm configuration runs `poddy-agent` as a sidecar, so custom
workspace images do not need to include it. With `ssh.agent.sidecar=false`,
include `/usr/local/bin/poddy-agent` in the image; `poddy-ssh-start` starts it
before `sshd`.

The persistent home volume is mounted at `/home/dev`. It shadows the image's
home directory, so put shell defaults in `/etc/skel` (seeded on first start) or
`/etc/profile.d`. `poddy-ssh-start` copies `.bashrc`, `.profile`, and
`.bash_logout` from `/etc/skel` when they are missing.

The repository uses these Dockerfiles:

| Image | Dockerfile |
|-------|------------|
| Portal | `Dockerfile` |
| Workspace base | `Dockerfile.workspace-base` |
| Go workspace | `Dockerfile.workspace-go` |
| Node.js workspace | `Dockerfile.workspace-node` |
| Python workspace | `Dockerfile.workspace-python` |

### Dockerfile pattern

```dockerfile
ARG BASE_IMAGE=ghcr.io/your-org/poddy-workspace-base:latest
FROM ${BASE_IMAGE}

# Install your toolchain
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
 && apt-get install -y --no-install-recommends nodejs \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /home/dev
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
```

**Build & publish (custom images only):**
```bash
docker build -f Dockerfile.workspace-base -t ghcr.io/your-org/poddy-workspace-base:1 .
docker push ghcr.io/your-org/poddy-workspace-base:1

docker build -f examples/workspaces/node/Dockerfile \
  --build-arg BASE_IMAGE=ghcr.io/your-org/poddy-workspace-base:1 \
  -t ghcr.io/your-org/poddy-workspace-node:1 .
docker push ghcr.io/your-org/poddy-workspace-node:1
```

### Test locally

```bash
docker run --rm -p 2222:22 \
  -e SSH_AUTHORIZED_KEY="$(cat ~/.ssh/id_ed25519.pub)" \
  --entrypoint /usr/local/bin/poddy-ssh-start \
  ghcr.io/your-org/poddy-workspace-node:1

ssh -p 2222 dev@127.0.0.1
```

### VS Code in the browser

Poddy's defaults inject an
[OpenVSCode Server](https://github.com/gitpod-io/openvscode-server) sidecar when
the `browserVSCode` parameter is `"true"` (the built-in default). When the
sidecar is present, the workspace card shows a **VS Code Web** button that opens
a new browser tab. The portal authenticates the request, checks workspace
ownership, port-forwards to the sidecar, and proxies HTTP and WebSocket traffic.

The sidecar binds to `127.0.0.1:3000`, so it is only reachable through the
portal's port-forward, not from other pods or the internet. Set
`browserVSCode: "false"` to omit it. The container name must stay `code-server`;
the portal uses that name to decide whether to show the button, and the
`--server-base-path` value must match the portal path for the workspace.

Templates can override any part of the sidecar by defining a `code-server`
container. Only the fields they set replace the defaults:

```yaml
spec:
  containers:
    - name: code-server
      image: registry.example.com/mirror/openvscode-server:1
      resources:
        limits:
          memory: 2Gi
```

The default image comes from Docker Hub. If your cluster blocks Docker Hub,
mirror `gitpod/openvscode-server` to your registry and override the image as
above, or set `browserVSCode` to `"false"` to skip the sidecar. OpenVSCode
Server is MIT-licensed; the Gitpod image is only a published build, so mirroring
it under your own registry does not change the license.

### Image pull secrets

If your registry is private, add `imagePullSecrets` in the pod template:
```yaml
spec:
  imagePullSecrets:
    - name: my-registry-secret
  containers:
    ...
```
The Helm chart's top-level `imagePullSecrets` applies only to the portal and bastion pods. Use `workspace.imagePullSecrets` for every workspace pod, or add `imagePullSecrets` in the pod template for per-template credentials.

## Parameter handling details

- The UI sends `parameters` from the create dialog (only declared parameters).
- Deprecated API fields `cpu`, `memory`, `storage` in `CreateRequest` override parameters of the same name (for backward compatibility).
- `storage` and `storageClass` are special: if `storage` is non-empty, the portal creates a PVC named `poddy-<workspace>` in the workspace namespace using the given `storageClass` (or default). The defaults mount it at `/home/dev`; a template can override the `workspace` volume or mount. Set `workspace.storageClass` in the Helm chart (or `PODDY_STORAGE_CLASS`) to apply a default when the template omits the parameter or leaves it empty.
- Set `workspace.imageRegistry` in the Helm chart (or `PODDY_IMAGE_REGISTRY`) as a global fallback for workspace and sidecar images. Set `workspace.imagePullSecrets` (or `PODDY_WORKSPACE_IMAGE_PULL_SECRETS`) for a private mirror; the Secrets must exist in the workspace namespace.
- Set `workspace.imageRegistryMap` (or `PODDY_IMAGE_REGISTRY_MAP`) for per-host mirrors. Keys are source hosts (`ghcr.io`, `docker.io`, `localhost:5000`) or `*`; values are target prefixes. Exact host wins, then `*`, then `imageRegistry`. An empty target leaves that host untouched. Poddy replaces the source host and keeps the repository path; already-rewritten images are left unchanged. Example:

  ```yaml
  workspace:
    imageRegistry: mirror.example.com/cache
    imageRegistryMap:
      ghcr.io: mirror.example.com/ghcr
      docker.io: mirror.example.com/docker
      "*": mirror.example.com/cache
  ```
- If you don't want a PVC, set `storage` default to `""` in your template. The defaults then use an `emptyDir`; a template can still define additional PVCs of its own.

## Validation & debugging

- `go test ./...` runs template validation tests.
- Start the portal with your `-templates-dir`; invalid templates log errors at startup.
- Create a workspace via the UI; check `kubectl -n <ns> get pods` and `kubectl -n <ns> logs poddy-<name>-0`.
- The pod template is rendered server-side; if rendering fails, the API returns 400 with the error.

## Example: Node.js workspace

The `poddy-templates` chart ships a default `node` template using `ghcr.io/slimaineb/poddy-workspace-node:latest`.
The files in `examples/templates/node/` and `examples/workspaces/node/` show how
to override it with a private image.

See `examples/templates/node/` and `examples/workspaces/node/`:
- `template.yaml` declares `cpu`, `memory`, `storage`, `storageClass`, `browserVSCode`.
- `pod.yaml` adds `NODE_ENV=development`.
- `Dockerfile` layers Node.js 22 on `poddy-workspace-base`.

To use it:
```bash
./poddy -templates-dir ./examples/templates ...
```
Or push it as a labeled ConfigMap / add it to the `poddy-templates` chart.
