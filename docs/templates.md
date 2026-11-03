# Creating templates and images

## How templates work

A **template** is a pair of files:

```
my-template/
├── template.yaml   # metadata, parameters, default image
└── pod.yaml        # Go template → corev1.Pod
```

The portal loads templates from these sources (later sources override embedded templates with the same name):

1. **Embedded** templates in `internal/templates/files/` (`base`, `go`).
2. **Local directory** given by `-templates-dir` (or `PODDY_TEMPLATES_DIR`). It accepts either subdirectories (`node/template.yaml`, `node/pod.yaml`) or flat files (`node.template.yaml`, `node.pod.yaml`). The portal watches the directory and reloads on change.
3. **Kubernetes ConfigMaps** labeled `poddy.io/template=true` in the workspace namespace. Each ConfigMap contains `template.yaml` and `pod.yaml`. The portal watches them and reloads in place, so templates can be pushed at any time without restarting the portal.

For ConfigMaps, the template name comes from the `poddy.io/template-name` label, or from the ConfigMap name when the label is absent. It must match `name` in `template.yaml`.

ConfigMaps can also carry `poddy.io/template-version` and `poddy.io/template-active=true`. This lets several versions of the same template coexist; the portal serves the active one. See [Versioning and rollback](#versioning-and-rollback).

## template.yaml schema

```yaml
name: node                    # required; must match directory/key/ConfigMap name
displayName: Node.js          # optional; defaults to name
description: Node workspace   # optional
image: ghcr.io/org/poddy-workspace-node:latest  # required
version: "1.2.0"              # optional; ConfigMap label overrides it
parameters:                   # optional list
  - name: cpu
    description: CPU request
    default: "1"
    required: false
  - name: memory
    description: Memory request
    default: 2Gi
  - name: storage
    description: PVC size (empty = ephemeral)
    default: 10Gi
  - name: storageClass
    description: StorageClass name (empty = default)
    default: ""
links:                        # optional list; rendered on workspace cards
  - name: JetBrains Gateway
    url: "jetbrains-gateway://connect#host={sshHost}&port={sshPort}&user={user}"
    icon: jetbrains
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

`pod.yaml` is a Go `text/template` rendered into a `corev1.Pod`. The portal calls `template.Render(Data)` where `Data` contains:

| Field | Type | Description |
|-------|------|-------------|
| `.Name` | string | Workspace name (user-provided) |
| `.Namespace` | string | Workspace namespace |
| `.PodName` | string | `poddy-<name>` |
| `.PVCName` | string | Same as `.PodName` (PVC name if created) |
| `.Image` | string | Template image (or request override) |
| `.Parameters` | map[string]string | Resolved parameters (defaults + user input) |
| `.Storage` | string | Same as `.Parameters["storage"]` |
| `.AuthorizedKeys` | string | Newline-separated SSH public keys for the workspace owner plus admin keys (never the full global key set) |
| `.SSHCommand` | string | `/usr/local/bin/poddy-ssh-start` |
| `.WorkspacePath` | string | `/workspace` |
| `quote` | func | `strconv.Quote` — use for env values, paths |

External auth env vars are added to the `workspace` container after rendering; they are recorded in the `poddy.io/external-auth-env` annotation so tokens are not stored in the stopped-state ConfigMap.
| `.Param "cpu"` | method | `Data.Param("cpu")` — same as `.Parameters.cpu` |

**Requirements:**
- The template **must define a container named `workspace`**. This is the target for exec/port-forward.
- Use `{{ .SSHCommand | quote }}` as the container `command` and set `env: [{name: SSH_AUTHORIZED_KEY, value: {{ .AuthorizedKeys | quote }}]` so the bastion/VS Code can authenticate. The portal fills `.AuthorizedKeys` with the workspace owner's key plus admin keys; it does not inject every global key.
- Mount the workspace volume at `{{ .WorkspacePath | quote }}`. Use a PVC when `.Storage` is non-empty, else `emptyDir`:
  ```yaml
  volumes:
    - name: workspace
      {{- if .Storage }}
      persistentVolumeClaim:
        claimName: {{ .PVCName | quote }}
      {{- else }}
      emptyDir: {}
      {{- end }}
  ```
- The portal overwrites `metadata.name`, `metadata.namespace`, adds labels (`app.kubernetes.io/name=poddy`, `poddy.io/workspace=<name>`, `poddy.io/template=<template>`), and sets `restartPolicy: Always` if empty.
- `missingkey=error` is set — every `.Parameters.foo` must be declared in `template.yaml`.

**Example (minimal):**
```yaml
apiVersion: v1
kind: Pod
metadata:
  labels:
    poddy.io/template: my-template
spec:
  automountServiceAccountToken: false
  securityContext:
    fsGroup: 1000
  containers:
    - name: workspace
      image: {{ .Image | quote }}
      imagePullPolicy: IfNotPresent
      command: [{{ .SSHCommand | quote }}]
      env:
        - name: SSH_AUTHORIZED_KEY
          value: {{ .AuthorizedKeys | quote }}
      workingDir: {{ .WorkspacePath | quote }}
      ports:
        - name: ssh
          containerPort: 22
      resources:
        requests:
          cpu: {{ .Parameters.cpu | quote }}
          memory: {{ .Parameters.memory | quote }}
      volumeMounts:
        - name: workspace
          mountPath: {{ .WorkspacePath | quote }}
  volumes:
    - name: workspace
      {{- if .Storage }}
      persistentVolumeClaim:
        claimName: {{ .PVCName | quote }}
      {{- else }}
      emptyDir: {}
      {{- end }}
```

## Built-in templates

| Name | Image | Parameters | Notes |
|------|-------|------------|-------|
| `base` | `poddy/workspace:latest` | cpu, memory, storage, storageClass | Generic Linux |
| `go` | `poddy/workspace:latest` | cpu, memory, storage, storageClass | Adds `GOPATH=/home/dev/go`, larger defaults |

Both mount the workspace volume at `/workspace` and run the SSH entrypoint.

## Adding external templates (local)

```bash
./poddy -templates-dir ./my-templates ...
```
Structure:
```
my-templates/
├── node/
│   ├── template.yaml
│   └── pod.yaml
└── python/
    ├── template.yaml
    └── pod.yaml
```
The portal watches the directory and reloads templates after changes; no restart is needed. Flat `<name>.template.yaml` / `<name>.pod.yaml` files are also supported.

## Pushing templates to Kubernetes

Templates are deliberately separate from the portal Helm chart. Use the `poddy-templates` chart, or apply labeled ConfigMaps directly.

### Option A: separate Helm chart

```bash
helm upgrade --install poddy-templates deploy/helm/poddy-templates \
  -n poddy-system -f templates-values.yaml
```

```yaml
# templates-values.yaml
templates:
  node:
    active: "2.0.0"
    versions:
      "1.0.0":
        template: |
          name: node
          displayName: Node.js
          image: ghcr.io/org/poddy-workspace-node:1
          parameters:
            - name: cpu
              default: "1"
        pod: |
          apiVersion: v1
          kind: Pod
          spec:
            containers:
              - name: workspace
                image: {{ .Image | quote }}
                command: [{{ .SSHCommand | quote }}]
                env:
                  - name: SSH_AUTHORIZED_KEY
                    value: {{ .AuthorizedKeys | quote }}
      "2.0.0":
        template: |
          name: node
          displayName: Node.js
          image: ghcr.io/org/poddy-workspace-node:2
        pod: |
          apiVersion: v1
          kind: Pod
          spec:
            containers:
              - name: workspace
                image: {{ .Image | quote }}
                command: [{{ .SSHCommand | quote }}]
```

The chart creates one ConfigMap per template version with `poddy.io/template=true`. Install it in the portal's workspace namespace (`workspaceNamespace`, usually the release namespace). A single unversioned `template`/`pod` entry is still supported.

### Option B: apply a ConfigMap directly

This is the closest to the Grafana dashboard pattern: push a labeled ConfigMap whenever you want.

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: node-template
  namespace: poddy-system
  labels:
    poddy.io/template: "true"
    poddy.io/template-name: node
    poddy.io/template-version: "1.0.0"
    poddy.io/template-active: "true"
data:
  template.yaml: |
    name: node
    displayName: Node.js
    image: ghcr.io/org/poddy-workspace-node:1
    parameters:
      - name: cpu
        default: "1"
  pod.yaml: |
    apiVersion: v1
    kind: Pod
    spec:
      containers:
        - name: workspace
          image: {{ .Image | quote }}
          command: [{{ .SSHCommand | quote }}]
          env:
            - name: SSH_AUTHORIZED_KEY
              value: {{ .AuthorizedKeys | quote }}
```

```bash
kubectl -n poddy-system apply -f node-template.yaml
kubectl -n poddy-system delete configmap node-template   # remove it
```

The portal reloads within a few seconds. If the update is invalid, it logs the error and keeps the previous catalog active.

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
helm upgrade poddy-templates deploy/helm/poddy-templates -n poddy-system \
  -f templates-values.yaml --set-string templates.node.active=1.0.0
```

Rollback with raw ConfigMaps: move the label to the old version:

```bash
kubectl -n poddy-system label configmap node-1.0.0 poddy.io/template-active=true --overwrite
kubectl -n poddy-system label configmap node-2.0.0 poddy.io/template-active-
```

The portal picks up the change through the watch. Old ConfigMaps can stay for history or be deleted once no longer needed. Workspaces already created keep their rendered spec; new workspaces use the active version.

### Operator checklist

1. Build and push the workspace image, or reuse an existing image such as `poddy/workspace:latest`.
2. Add the template to the separate `poddy-templates` release, or `kubectl apply` a labeled ConfigMap. Use `poddy.io/template-version` and `poddy.io/template-active` to keep versions for rollback.
3. Check the portal logs:
   ```bash
   kubectl -n poddy-system logs deploy/poddy --tail=100
   ```
4. Open **Templates** in the portal, search for the new name, and set access if needed. A template with no RBAC entry is public.

No portal restart or `helm upgrade` of the portal is required. If the workspace image is private, add `imagePullSecrets` inside the template's `pod` spec; the portal chart's top-level `imagePullSecrets` only applies to the portal pod.

**ConfigMap size limit**: 1 MiB per ConfigMap. Templates are small; split large templates into separate ConfigMaps or bake them into a custom portal image.

## Building custom workspace images

### Base image contract

`poddy/workspace-base` provides:
- OpenSSH server on port 22
- `dev` user (UID 1000), passwordless sudo
- `/workspace` directory owned by `dev:dev`
- `/usr/local/bin/poddy-ssh-start` — writes `SSH_AUTHORIZED_KEY` to `/home/dev/.ssh/authorized_keys` and starts `sshd`
- `CMD ["/usr/sbin/sshd", "-D", "-e"]` (overridden by pod `command`)

The pod template uses `command: [{{ .SSHCommand | quote }}]` to run the entrypoint.

### Dockerfile pattern

```dockerfile
ARG BASE_IMAGE=ghcr.io/your-org/poddy-workspace-base:latest
FROM ${BASE_IMAGE}

# Install your toolchain
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
 && apt-get install -y --no-install-recommends nodejs \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
```

**Build & push:**
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

### Image pull secrets

If your registry is private, add `imagePullSecrets` in the pod template:
```yaml
spec:
  imagePullSecrets:
    - name: my-registry-secret
  containers:
    ...
```
The Helm chart's `imagePullSecrets` applies only to the portal pod, not workspaces.

## Parameter handling details

- The UI sends `parameters` from the create dialog (only declared parameters).
- Deprecated API fields `cpu`, `memory`, `storage` in `CreateRequest` override parameters of the same name (for backward compatibility).
- `storage` and `storageClass` are special: if `storage` is non-empty, the portal creates a PVC named `poddy-<workspace>` in the workspace namespace using the given `storageClass` (or default). The pod template decides whether to mount it (see `.Storage`).
- If you don't want a PVC, set `storage` default to `""` in your template and omit the `persistentVolumeClaim` volume.

## Validation & debugging

- `go test ./...` runs template validation tests.
- Start the portal with your `-templates-dir`; invalid templates log errors at startup.
- Create a workspace via the UI; check `kubectl -n <ns> get pods` and `kubectl -n <ns> logs poddy-<name>`.
- The pod template is rendered server-side; if rendering fails, the API returns 400 with the error.

## Example: Node.js workspace

See `examples/templates/node/` and `examples/workspaces/node/`:
- `template.yaml` declares `cpu`, `memory`, `storage`, `storageClass`.
- `pod.yaml` adds `NODE_ENV=development`.
- `Dockerfile` layers Node.js 22 on `poddy-workspace-base`.

To use it:
```bash
./poddy -templates-dir ./examples/templates ...
```
Or push it as a labeled ConfigMap / add it to the `poddy-templates` chart.
