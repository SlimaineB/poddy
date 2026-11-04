# Getting started

This is the shortest path to a running workspace on a local Kubernetes or k3s
cluster. It uses the published GHCR images and Helm, so you do **not** need Go,
Docker, or a local image build.

!!! tip "Just want the production path?"
    See [Kubernetes deployment](deploy.md) for TLS, a LoadBalancer, multiple
    replicas, and a stable SSH host key.

## Before you start

You need:

- A running Kubernetes or k3s cluster
- `kubectl`
- Helm 3.13+
- `ssh-keygen` and `openssl`
- Optional: VS Code with the **Remote-SSH** extension

The commands below use your current kubeconfig context. Check it first:

```bash
kubectl config current-context
```

Clone the repository so Helm can use the chart:

```bash
git clone https://github.com/SlimaineB/poddy.git
cd poddy
```

## 1. Create the namespace and secrets

```bash
kubectl create namespace poddy-system --dry-run=client -o yaml | kubectl apply -f -

TOKEN="$(openssl rand -hex 32)"
kubectl -n poddy-system create secret generic poddy-auth \
  --from-literal=token="$TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -
```

Generate an SSH key for the bastion. The `user:token` comment binds the key to
the token identity used by this local setup:

```bash
ssh-keygen -t ed25519 -N '' -C 'user:token' -f ./poddy_key
ssh-keygen -t ed25519 -N '' -f ./poddy_host_ed25519

kubectl -n poddy-system create secret generic poddy-host-key \
  --from-file=ssh_host_ed25519_key=./poddy_host_ed25519 \
  --dry-run=client -o yaml | kubectl apply -f -
```

## 2. Install poddy

The chart pulls the portal/bastion image and the built-in workspace images from
GHCR. This example exposes the web UI and SSH through `kubectl port-forward`, so
it works without a LoadBalancer or ingress:

```bash
helm upgrade --install poddy ./deploy/helm/poddy \
  --namespace poddy-system \
  --set image.repository=ghcr.io/slimaineb/poddy \
  --set image.tag=latest \
  --set auth.existingSecret=poddy-auth \
  --set ssh.service.type=ClusterIP \
  --set ssh.publicHost=127.0.0.1 \
  --set ssh.publicPort=2222 \
  --set-string ssh.authorizedKeys[0]="$(cat ./poddy_key.pub)" \
  --set ssh.existingHostKeySecret=poddy-host-key \
  --set workspaceNamespace=poddy-workspaces \
  --set createWorkspaceNamespace=true
```

Wait for the portal and bastion:

```bash
kubectl -n poddy-system rollout status deploy/poddy
kubectl -n poddy-system rollout status deploy/poddy-bastion
```

If the GHCR packages are private, make them public for this quick start. The
chart's `imagePullSecrets` only covers the portal and bastion; workspace
templates need their own `imagePullSecrets` in the pod spec.

## 3. Start the local port-forwards

Keep each command running in its own terminal:

```bash
kubectl -n poddy-system port-forward svc/poddy 8080:80
```

```bash
kubectl -n poddy-system port-forward svc/poddy-ssh 2222:2222
```

## 4. Open the portal

Read the generated token:

```bash
kubectl -n poddy-system get secret poddy-auth \
  -o jsonpath='{.data.token}' | base64 -d
```

Open <http://127.0.0.1:8080> and sign in with that token.

## 5. Create your first workspace

1. Click **New workspace**.
2. Pick **Base** or **Go**.
3. Adjust CPU, memory, or storage if you want.
4. Click **Create**.
5. Wait for the workspace to reach **Running**.

## 6. Connect

Use the buttons on the workspace card:

- **Terminal** opens a browser shell.
- **SSH** copies a ready-to-use SSH config. It uses the bastion as a
  `ProxyJump`, so you do not need direct access to the pod.
- **VS Code** opens Remote-SSH on `/home/dev`.

For the SSH/VS Code path, load the private key once:

```bash
ssh-add ./poddy_key
```

## What to try next

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
| Workspace stays in `ImagePullBackOff` | GHCR packages are private or not accessible | Make the packages public or add an image pull secret |
| `pods is forbidden` or namespace errors | Portal points at the wrong namespace | Check `workspaceNamespace` and the RoleBinding in that namespace |
| SSH connection refused | The SSH port-forward is not running | Start `kubectl -n poddy-system port-forward svc/poddy-ssh 2222:2222` |
| VS Code cannot connect | The key is not loaded or the SSH config was not saved | Run `ssh-add ./poddy_key` and save the config from the **SSH** dialog |
| Login fails | Wrong token | Read the token from the `poddy-auth` secret again |
