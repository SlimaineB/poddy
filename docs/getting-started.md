# Getting started

The shortest path to a running workspace on a local Kubernetes or k3s cluster.
It uses the published GHCR images and Helm, so you do **not** need Go, Docker,
or a local image build.

You need:

- A running Kubernetes or k3s cluster
- `kubectl`
- Helm 3.13+
- Optional: VS Code with **Remote-SSH**, Cursor with Remote-SSH, PyCharm via JetBrains Gateway, or a browser for JupyterLab

The commands below use your current kubeconfig context:

```bash
kubectl config current-context
```

## 1. Create the namespace and the portal secret

```bash
kubectl create namespace poddy-system --dry-run=client -o yaml | kubectl apply -f -

TOKEN="$(openssl rand -hex 32)"
kubectl -n poddy-system create secret generic poddy-auth \
  --from-literal=token="$TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -
```

This token signs you in the first time. For a team, replace it with OIDC later
(see the [guided tour](guided-tour.md)).

## 2. Install poddy

The chart pulls the portal/bastion image and the built-in workspace images from
GHCR. This example exposes the web UI and SSH through `kubectl port-forward`, so
it works without a LoadBalancer or ingress:

```bash
helm upgrade --install poddy oci://ghcr.io/slimaineb/charts/poddy \
  --version 0.1.3 \
  --namespace poddy-system \
  --set image.repository=ghcr.io/slimaineb/poddy \
  --set image.tag=latest \
  --set auth.existingSecret=poddy-auth \
  --set ssh.service.type=ClusterIP \
  --set ssh.publicHost=127.0.0.1 \
  --set ssh.publicPort=2222 \
  --set workspaceNamespace=poddy-workspaces \
  --set createWorkspaceNamespace=true
```

Wait for the portal and bastion:

```bash
kubectl -n poddy-system rollout status deploy/poddy
kubectl -n poddy-system rollout status deploy/poddy-bastion
```

## 3. Open the portal

```bash
kubectl -n poddy-system port-forward svc/poddy 8080:80
```

Read the token:

```bash
kubectl -n poddy-system get secret poddy-auth \
  -o jsonpath='{.data.token}' | base64 -d
```

Open <http://127.0.0.1:8080> and sign in with that token.

## 4. Create your first workspace

1. Click **New workspace**.
2. Pick **Base**, **Go**, **Node.js**, or **Python**.
3. Adjust CPU, memory, or storage if you want.
4. Click **Create**.
5. Wait for the workspace to reach **Running**.

Python workspaces include a JupyterLab sidecar on port `8888`; open it with the
**JupyterLab** button on the workspace card.

## 5. Connect

Use the buttons on the workspace card:

- **Terminal** opens a browser shell in the workspace user home (`/home/dev`).
- **VS Code** opens Remote-SSH on `/home/dev` in a new window. In cluster mode
  the Poddy extension enrolls a dedicated machine key and writes the SSH config
  automatically; install the extension once per machine.
- **Cursor** uses the same extension and Remote-SSH flow. Install the same VSIX
  in Cursor, then click **Cursor**; the portal opens a signed `cursor://` URL.
- **PyCharm** opens JetBrains Gateway on `/home/dev`. Copy the **SSH** config
  once and enable **Parse config file ~/.ssh/config** in Gateway. The generated
  config uses `ProxyCommand`, which Gateway supports.
- **JupyterLab** opens port `8888` in a new tab through the portal proxy. The
  workspace image must run JupyterLab on that port.
- **SSH** (manual fallback) copies a ready-to-use SSH config. It uses the
  bastion as a `ProxyCommand`, so you do not need direct access to the pod.
- **VS Code Web** opens the browser editor on `/home/dev` in a new tab when
  browser VS Code is enabled (`browserVSCode`, default `true` for the built-in
  templates).
- **Ports** opens a dedicated ports window and lists TCP ports listening in the
  pod; the same list is available in workspace details. Click **Open** to proxy
  an app through the portal in a new tab, or enter a port manually.

## What to try next

- [Guided tour](guided-tour.md): OIDC, PostgreSQL, and installing in production.
- [Workspace templates](templates.md): add your own image, parameters, and
  action buttons.
- [External authentication](external-auth.md): inject a refreshed GitHub or
  GitLab token into workspaces.
- [OIDC / SSO](oidc.md): replace the shared token with your identity provider.
- [RBAC and ownership](rbac.md): add admins, teams, and per-template access.
- [Kubernetes deployment](deploy.md): run the portal with TLS and multiple
  replicas.
- [Development](development.md): build the portal and workspace images from
  source.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| Workspace stays in `ImagePullBackOff` | Image not accessible from the cluster | Check the image/tag and that nodes can reach GHCR |
| `pods is forbidden` or namespace errors | Portal points at the wrong namespace | Check `workspaceNamespace` and the RoleBinding in that namespace |
| SSH connection refused | The SSH port-forward is not running | Start `kubectl -n poddy-system port-forward svc/poddy-ssh 2222:2222` |
| VS Code or Cursor cannot connect | The Poddy extension is not installed, the workspace is still propagating keys, or the SSH fallback was used without saving the config | Install the extension in that editor, retry after the workspace is `Running`, or save the config from the **SSH** dialog |
| PyCharm cannot connect | Gateway is not parsing the SSH config, or the config still uses `ProxyJump` | Enable **Parse config file ~/.ssh/config** and save the generated config with `ProxyCommand` from the **SSH** dialog |
| Login fails | Wrong token | Read the token from the `poddy-auth` secret again |
