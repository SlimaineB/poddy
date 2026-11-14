# poddy-templates Helm chart

Workspace templates for the poddy portal, managed as a separate Helm release. Each template is its own labeled ConfigMap. The portal watches these ConfigMaps and reloads its catalog in place, so pushing a template does **not** restart the portal.

The portal does not embed any templates. Install this chart (or apply equivalent labeled ConfigMaps) to get the default `base`, `go`, `node` and `python` templates; without templates the portal starts but cannot create workspaces.

## Install

The chart is published as an OCI artifact in GHCR. It creates the template
ConfigMaps in the namespace the portal watches (`workspaceNamespace`, which is
`poddy-workspaces` in the quick start). That is **not** necessarily the portal
release namespace.

By default the chart writes to its own release namespace. When the portal and
its workspace namespace differ, either install the chart in the workspace
namespace or set `namespace`:

```bash
# Install directly in the portal's workspace namespace.
helm upgrade --install poddy-templates oci://ghcr.io/slimaineb/charts/poddy-templates \
  -n poddy-workspaces --version 0.1.6 -f templates-values.yaml

# Or keep the release elsewhere and point it at the workspace namespace.
helm upgrade --install poddy-templates oci://ghcr.io/slimaineb/charts/poddy-templates \
  -n poddy-system --version 0.1.6 --set namespace=poddy-workspaces -f templates-values.yaml
```

If the GHCR package is private, authenticate first:

```bash
helm registry login ghcr.io
```

Or install from a source checkout:

```bash
helm upgrade --install poddy-templates deploy/helm/poddy-templates \
  -n poddy-workspaces -f templates-values.yaml
```

Replace `0.1.6` with the published chart version when a new release is cut.

## Default templates

The chart ships the default `base`, `go`, `node` and `python` templates and
deploys them as ConfigMaps (`builtins.enabled=true`, the default). The portal
does not embed any templates: without this chart (or equivalent labeled
ConfigMaps) it starts with an empty catalog and no workspace can be created.
Set `builtins.enabled=false` only when you manage every template yourself.

The chart's copies live in `deploy/helm/poddy-templates/files/` and mirror
`internal/templates/files/` in the Go source. After changing the source
templates, run `make sync-templates` and commit both; `make check-templates`
(also run in CI) fails when they drift.

The portal discovers ConfigMaps with:

```yaml
labels:
  poddy.io/template: "true"
  poddy.io/template-name: node   # optional; defaults to the ConfigMap name
```

The ConfigMap data keys are `template.yaml` and `pod.yaml`. `template.yaml`'s `name` must match the template name (label or ConfigMap name).

## Versioning and rollback

Keep multiple ConfigMaps for the same template and mark exactly one as active:

```yaml
labels:
  poddy.io/template: "true"
  poddy.io/template-name: node
  poddy.io/template-version: "1.0.0"
  poddy.io/template-active: "true"   # only on the served version
```

The chart supports this directly:

```yaml
templates:
  node:
    active: "2.0.0"
    versions:
      "1.0.0":
        template: |
          name: node
          image: ghcr.io/org/poddy-workspace-node:1
        pod: |
          apiVersion: v1
          kind: Pod
      "2.0.0":
        template: |
          name: node
          image: ghcr.io/org/poddy-workspace-node:2
        pod: |
          apiVersion: v1
          kind: Pod
```

Rollback by changing `active` and upgrading the chart:

```bash
helm upgrade poddy-templates deploy/helm/poddy-templates -n poddy-workspaces \
  -f templates-values.yaml --set-string templates.node.active=1.0.0
```

Or, with raw ConfigMaps, move the label without restarting the portal:

```bash
kubectl -n poddy-workspaces label configmap node-1.0.0 poddy.io/template-active=true --overwrite
kubectl -n poddy-workspaces label configmap node-2.0.0 poddy.io/template-active-
```

The portal serves the active version, exposes it in the Templates page, and reloads in place.

## Push a template without Helm

Apply a labeled ConfigMap whenever you want; no portal restart is needed:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: node-template
  namespace: poddy-workspaces
  labels:
    poddy.io/template: "true"
    poddy.io/template-name: node
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
          env:
            - name: NODE_ENV
              value: development
```

Poddy merges `pod.yaml` over its internal pod defaults (SSH, workspace volume,
resources, security context, browser VS Code sidecar). A template can be as
small as `apiVersion: v1`, `kind: Pod`, `spec: {}`, and can still override or
add any field. See [docs/templates.md](../../../docs/templates.md) for the merge
rules.

```bash
kubectl -n poddy-workspaces apply -f node-template.yaml
kubectl -n poddy-workspaces delete configmap node-template   # remove template
```

The portal reloads within a few seconds. An invalid update is rejected and the previous catalog stays active; check `kubectl -n poddy-system logs deploy/poddy`.

## GitOps and one repository per team

Templates are plain Kubernetes ConfigMaps, so they fit Argo CD, Flux, or any
GitOps controller. Keep templates in the team's own repository and sync them
into that team's workspace namespace; the portal watches the namespace and
reloads without a restart.

Rules for multi-team setups:

- **One logical name per namespace.** The logical name is
  `poddy.io/template-name`, not the ConfigMap name. Two active ConfigMaps with
  the same logical name make the catalog ambiguous and the portal rejects the
  reload, keeping the previous catalog. Team releases should set
  `builtins.enabled=false` and ship only their own templates, or use unique
  names.
- **Use a distinct release name per team** (for example
  `helm upgrade --install team-a-templates ...`). The release name is part of
  the ConfigMap name, so releases do not overwrite each other's objects.
- **Removing a template from Git** deletes its ConfigMap (Helm or Argo pruning);
  the portal drops it from the catalog. Existing workspaces keep their spec.
- **Without Helm**, commit the labeled ConfigMaps directly and apply them with
  Kustomize; same labels, same behavior.
- **One repository rendering for several namespaces**: set `namespace` per
  release instead of relying on the release namespace.

Example team values (`builtins.enabled=false`):

```yaml
builtins:
  enabled: false
templates:
  team-api:
    template: |
      name: team-api
      image: ghcr.io/acme/poddy-workspace-api:1
    pod: |
      apiVersion: v1
      kind: Pod
      spec: {}
```

## Values

| Parameter | Default | Description |
|-----------|---------|-------------|
| `namespace` | release namespace | Namespace where the ConfigMaps are created (must be the portal's workspace namespace). |
| `builtins.enabled` | `true` | Deploy the default `base`/`go`/`node`/`python` templates. |
| `nameOverride` | `""` | Override the chart name. |
| `fullnameOverride` | `""` | Override the generated ConfigMap prefix. |
| `templates` | `{}` | Map of template name to `{template, pod}` or `{active, versions}`. |

See [docs/templates.md](../../../docs/templates.md) for the full template contract.
