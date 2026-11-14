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
  -n poddy-workspaces --version 0.1.5 -f templates-values.yaml

# Or keep the release elsewhere and point it at the workspace namespace.
helm upgrade --install poddy-templates oci://ghcr.io/slimaineb/charts/poddy-templates \
  -n poddy-system --version 0.1.5 --set namespace=poddy-workspaces -f templates-values.yaml
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

Replace `0.1.5` with the published chart version when a new release is cut.

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
helm upgrade poddy-templates deploy/helm/poddy-templates -n poddy-system \
  -f templates-values.yaml --set-string templates.node.active=1.0.0
```

Or, with raw ConfigMaps, move the label without restarting the portal:

```bash
kubectl -n poddy-system label configmap node-1.0.0 poddy.io/template-active=true --overwrite
kubectl -n poddy-system label configmap node-2.0.0 poddy.io/template-active-
```

The portal serves the active version, exposes it in the Templates page, and reloads in place.

## Push a template without Helm

Apply a labeled ConfigMap whenever you want; no portal restart is needed:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: node-template
  namespace: poddy-system
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
kubectl -n poddy-system apply -f node-template.yaml
kubectl -n poddy-system delete configmap node-template   # remove template
```

The portal reloads within a few seconds. An invalid update is rejected and the previous catalog stays active; check `kubectl -n poddy-system logs deploy/poddy`.

## Values

| Parameter | Default | Description |
|-----------|---------|-------------|
| `nameOverride` | `""` | Override the chart name. |
| `fullnameOverride` | `""` | Override the generated ConfigMap prefix. |
| `templates` | `{}` | Map of template name to `{template, pod}` or `{active, versions}`. |

See [docs/templates.md](../../../docs/templates.md) for the full template contract.
