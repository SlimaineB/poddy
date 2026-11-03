# Getting started

This is the shortest path to a running workspace on a local k3s cluster. It
takes a few minutes and needs no cloud account, ingress, or OIDC provider.

!!! tip "Just want the production path?"
    See [Kubernetes deployment](deploy.md) for Helm, TLS, a stable SSH host
    key, and multiple replicas.

## Before you start

You need:

- Go 1.26+
- Docker
- `kubectl`
- A running k3s cluster

The commands below use your current kubeconfig context. Check it first:

```bash
kubectl config current-context
```

## 1. Create the workspace namespace

```bash
kubectl create namespace poddy
```

If the namespace already exists, skip this step.

## 2. Build and import the workspace images

The portal runs on your machine, but workspace pods run inside k3s. Import the
images into k3s so the pods do not need a registry:

```bash
docker build -f Dockerfile.workspace-base -t poddy/workspace-base:latest .
docker build -f Dockerfile.workspace -t poddy/workspace:latest .

docker save poddy/workspace-base:latest poddy/workspace:latest \
  | sudo k3s ctr images import -
```

## 3. Build the portal

```bash
go build -o poddy .
```

## 4. Start the portal

```bash
export KUBE_CONTEXT="$(kubectl config current-context)"

./poddy \
  -context "$KUBE_CONTEXT" \
  -namespace poddy \
  -token=dev-token \
  -ssh-addr=127.0.0.1:2222 \
  -ssh-public-host=127.0.0.1 \
  -ssh-public-port=2222
```

What these flags do:

| Flag | Purpose |
|------|---------|
| `-context` | kubeconfig context the portal uses |
| `-namespace` | namespace where workspace pods and PVCs are created |
| `-token` | shared login token; anyone with it is a global admin |
| `-ssh-addr` | local address for the SSH bastion |
| `-ssh-public-host` / `-ssh-public-port` | address shown to users in SSH and VS Code instructions |

!!! warning "The shared token is admin access"
    `-token` is fine for a local test. For a team deployment, use
    [OIDC / SSO](oidc.md) and keep the token only for automation.

## 5. Open the portal

Go to <http://127.0.0.1:8090> and enter `dev-token`.

## 6. Create your first workspace

1. Click **New workspace**.
2. Pick **Base** or **Go**.
3. Adjust CPU, memory, or storage if you want.
4. Click **Create**.
5. Wait for the workspace to reach **Running**.

## 7. Connect

Use the buttons on the workspace card:

- **Terminal** opens a browser shell.
- **SSH** copies a ready-to-use SSH config. It uses the bastion as a
  `ProxyJump`, so you do not need direct access to the pod.
- **VS Code** opens Remote-SSH on `/home/dev`. In cluster mode, save the SSH
  config once; it covers every workspace.

## What to try next

- [Workspace templates](templates.md): add your own image, parameters, and
  action buttons.
- [External authentication](external-auth.md): inject a refreshed GitHub or
  GitLab token into workspaces.
- [OIDC / SSO](oidc.md): replace the shared token with your identity provider.
- [RBAC and ownership](rbac.md): add admins, teams, and per-template access.
- [Kubernetes deployment](deploy.md): run the portal in the cluster with Helm.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| Workspace stays in `ImagePullBackOff` | Workspace image is not in k3s containerd | Re-run the image import command from step 2 |
| `pods is forbidden` or namespace errors | Portal points at the wrong context or namespace | Check `kubectl config current-context` and the `-context` / `-namespace` flags |
| SSH connection refused | Bastion is not listening | Check the portal output and the `-ssh-addr` port |
| VS Code cannot connect | SSH config was not saved | Use the **SSH** button and save the generated config under `~/.ssh/config` |
| Login fails | Wrong token | Use the value passed to `-token` |
