# poddy-templates Helm chart

Workspace templates for the poddy portal, managed as a separate Helm release. Each template is its own labeled ConfigMap. The portal watches these ConfigMaps and reloads its catalog in place, so pushing a template does **not** restart the portal.

## Install

Install this chart in the same namespace the portal uses for templates (`workspaceNamespace`, usually the portal release namespace):

```bash
helm upgrade --install poddy-templates deploy/helm/poddy-templates \
  -n poddy-system -f templates-values.yaml
```

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
          image: {{ .Image | quote }}
          command: [{{ .SSHCommand | quote }}]
          env:
            - name: SSH_AUTHORIZED_KEY
              value: {{ .AuthorizedKeys | quote }}
```

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
