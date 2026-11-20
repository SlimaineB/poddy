---
title: "Quick start"
description: "Install poddy on Kubernetes with Helm, create a workspace, and connect a terminal or IDE in minutes."
---

This is the shortest path from a Kubernetes cluster to a running poddy
workspace. It uses the published GHCR images and Helm, so you do not need Go,
Docker, or a local image build.

:::tip[Choose your path]
- **Full product demo** with SSO, Gitea, MinIO, and PostgreSQL: [Full demo](demo.md).
- **Local k3s walkthrough** with separate namespaces and NodePorts: [k3s test](k3s-test.md).
- **Production installation** with TLS, OIDC, HA, and backups: [Kubernetes deployment](deploy.md).
:::

## Before you begin

| Requirement | Why |
|-------------|-----|
| Kubernetes 1.28+ or k3s | Runs the portal, workspaces, and volumes. |
| PostgreSQL 13+ | Stores users, RBAC, quotas, access, encrypted tokens/secrets, shares, and audit events. |
| Helm 3.13+ | Installs the portal and templates charts. |
| `kubectl` | Creates secrets and checks the deployment. |
| `openssl` | Generates the portal token and database password. |
| A default StorageClass | Creates the persistent home volume for each workspace. |
| Optional: ingress controller | Required for HTTPS outside the quick start. |

The commands use your current kubeconfig context:

```bash
kubectl config current-context
```

## 1. Create the namespace and portal token

```bash
kubectl create namespace poddy-system --dry-run=client -o yaml | kubectl apply -f -

TOKEN="$(openssl rand -hex 32)"
kubectl -n poddy-system create secret generic poddy-auth \
  --from-literal=token="$TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -
```

Keep `$TOKEN` or read it back later. The shared token is useful for a first
test; for a team, replace it with [OIDC/SSO](oidc.md).

## 2. Create PostgreSQL

### Option A: development database in the cluster

Use this for a local test only.

```bash
PGPASSWORD="$(openssl rand -hex 16)"
helm upgrade --install poddy-postgres oci://registry-1.docker.io/bitnamicharts/postgresql \
  --namespace poddy-system \
  --set auth.username=poddy \
  --set auth.password="$PGPASSWORD" \
  --set auth.database=poddy

kubectl -n poddy-system create secret generic poddy-database \
  --from-literal=database-url="postgres://poddy:${PGPASSWORD}@poddy-postgres-postgresql:5432/poddy?sslmode=disable" \
  --dry-run=client -o yaml | kubectl apply -f -
```

### Option B: existing PostgreSQL

Create only the Secret. Use TLS in production.

```bash
kubectl -n poddy-system create secret generic poddy-database \
  --from-literal=database-url='postgres://poddy:secret@postgres.example.com:5432/poddy?sslmode=require' \
  --dry-run=client -o yaml | kubectl apply -f -
```

The portal runs embedded migrations on startup by default. See
[PostgreSQL control plane](postgres-schema.md) for schema, migration, and backup
details.

## 3. Install the portal

```bash
helm upgrade --install poddy oci://ghcr.io/slimaineb/charts/poddy \
  --namespace poddy-system \
  --version 0.1.10 \
  --set image.repository=ghcr.io/slimaineb/poddy \
  --set image.tag=latest \
  --set auth.existingSecret=poddy-auth \
  --set database.existingSecret=poddy-database \
  --set ssh.service.type=ClusterIP \
  --set ssh.publicHost=127.0.0.1 \
  --set ssh.publicPort=2222 \
  --set workspaceNamespace=poddy-workspaces \
  --set createWorkspaceNamespace=true \
  --wait --timeout 10m
```

This creates:

- the `poddy` Deployment, which also serves SSH and the agent broker;
- the `poddy-workspaces` namespace and its Roles/RoleBindings;
- a generated SSH CA and workspace agent key.

Check the rollout:

```bash
kubectl -n poddy-system get deploy,pods
kubectl -n poddy-system rollout status deploy/poddy --timeout=5m
```

## 4. Install the default templates

Templates are a separate release. The portal starts without them but cannot
create workspaces until they exist.

```bash
helm upgrade --install poddy-templates oci://ghcr.io/slimaineb/charts/poddy-templates \
  --namespace poddy-workspaces \
  --version 0.1.10
```

The release installs the `base`, `go`, `node`, and `python` templates as
labeled ConfigMaps. The portal watches them and reloads without a restart.

## 5. Open the portal

```bash
kubectl -n poddy-system port-forward svc/poddy 8080:80
```

Read the token in another terminal:

```bash
kubectl -n poddy-system get secret poddy-auth \
  -o jsonpath='{.data.token}' | base64 -d
```

Open <http://127.0.0.1:8080> and sign in with the token.

For desktop SSH or Remote-SSH, start the SSH port-forward in a third
terminal:

```bash
kubectl -n poddy-system port-forward svc/poddy-ssh 2222:2222
```

## 6. Create your first workspace

1. Click **New workspace**.
2. Pick **Base**, **Go**, **Node.js**, or **Python**.
3. Adjust CPU, memory, or storage if you want.
4. Click **Create** and wait for **Running**.
5. Open it with the buttons on the card.

The first workspace can take a little longer while the image is pulled.

## 7. Connect

| Button | What happens | What you need |
|--------|--------------|---------------|
| **Terminal** | Opens a browser shell in `/home/dev`. | Nothing. |
| **VS Code Web** | Opens the browser editor in a new tab. | Template with `browserVSCode: "true"` (default). |
| **VS Code** | Opens Remote-SSH through the Poddy extension. | Install the extension once on that machine. |
| **Cursor** | Same Remote-SSH flow in Cursor. | Install the same VSIX in Cursor. |
| **PyCharm** | Opens JetBrains Gateway with the SSH config. | Enable **Parse config file ~/.ssh/config** in Gateway. |
| **JupyterLab** | Opens port `8888` through the portal proxy. | Python template or another template with JupyterLab. |
| **SSH** | Copies a ready-to-use `ProxyCommand` config. | An SSH key added in **SSH keys**. |
| **Ports** | Lists listening ports and opens them through the authenticated proxy. | Nothing. |

For the best desktop experience, open **SSH keys** in the portal and add your
public key first (`~/.ssh/id_ed25519.pub`). The VS Code and Cursor buttons can
also enroll a dedicated key automatically through the extension.

See [Workspaces](workspaces.md) for lifecycle, auto-stop, sharing, and daily
usage.

## 8. Stop paying for idle workspaces

Use the workspace menu to **Stop** a workspace. The StatefulSet scales to zero
and the persistent volume remains. **Start** brings it back with the same home
directory.

You can also set an **Auto-stop schedule** with a timezone and daily
start/stop times. poddy only auto-starts a workspace if the schedule stopped it;
a manual stop stays stopped.

## Clean up the quick start

```bash
helm -n poddy-workspaces uninstall poddy-templates
helm -n poddy-system uninstall poddy
helm -n poddy-system uninstall poddy-postgres

kubectl delete namespace poddy-system
kubectl delete namespace poddy-workspaces
```

:::caution[Persistent data]
Deleting the workspace namespace deletes its PVCs. If you installed with
`createWorkspaceNamespace=true`, `helm uninstall poddy` also removes that
namespace and its volumes. In production, create the workspace namespace
yourself and set `createWorkspaceNamespace: false`.
:::

## What to do next

- [Product tour](guided-tour.md): how the pieces fit together.
- [Workspaces](workspaces.md): create, connect, stop, share, and troubleshoot.
- [Templates](templates.md): add your own image and parameters.
- [External authentication](external-auth.md): inject refreshed GitHub/GitLab
  tokens into workspaces.
- [OIDC/SSO](oidc.md): replace the shared token with your identity provider.
- [Operations](operations.md): upgrades, backups, monitoring, and security.
- [Kubernetes deployment](deploy.md): production values, TLS, and scaling.

## Quick troubleshooting

| Symptom | Check |
|---------|-------|
| Portal pod is `CrashLoopBackOff` | `kubectl -n poddy-system logs deploy/poddy`; usually the database URL, migrations, or a missing Secret. |
| `no templates` or no **New workspace** choices | Install `poddy-templates` in the workspace namespace and check the portal logs. |
| Workspace stays in `ImagePullBackOff` | The cluster cannot pull the image or tag; check registry access and `workspace.imagePullSecrets`. |
| Workspace stays `Pending` | No schedulable node or no StorageClass/PVC. Check `kubectl -n poddy-workspaces describe pod`. |
| SSH connection refused | Start the SSH port-forward, or expose the SSH Service in production. |
| Login fails | Read the token again from `poddy-auth`; if OIDC is enabled, check the callback URL. |

See [Troubleshooting](troubleshooting.md) for the full runbook.
