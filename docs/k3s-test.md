# Clean k3s test deployment

!!! tip "Want the shortest path?"
    See [Getting started](getting-started.md) if you just want a workspace
    running quickly. This page is the fuller local deployment with separate
    namespaces.

This guide installs a test deployment with the control plane and workspaces in
**separate namespaces**. It uses the shared token for login (no OIDC), a stable
SSH host key, and NodePort services so it works on a local k3s cluster without
a cloud load balancer. The example also enables an optional Traefik ingress.

## Layout

| Component | Namespace | Notes |
|-----------|-----------|-------|
| Portal and bastion Deployments | `poddy-system` | Helm release `poddy` |
| Workspace pods + PVCs | `poddy-workspaces` | Dedicated namespace |
| Templates | `poddy-workspaces` | Default `base`/`go`/`node`/`python` templates from the separate `poddy-templates` chart |

The portal and bastion Roles/RoleBindings are created in `poddy-workspaces`;
both ServiceAccounts stay in `poddy-system`. Neither can access other
namespaces.

## 1. Build and import the portal image

```bash
docker build -t poddy:clean .
docker save poddy:clean | sudo k3s ctr images import -
```

Use `image.pullPolicy: IfNotPresent` in the values below so k3s uses the
imported image.

## 2. Create namespaces

```bash
kubectl create namespace poddy-system
kubectl create namespace poddy-workspaces
```

## 3. Create secrets

### Access token

```bash
TOKEN="$(openssl rand -hex 32)"
kubectl -n poddy-system create secret generic poddy-auth \
  --from-literal=token="$TOKEN"
```

The token identity is `token`. This test sets `auth.tokenAdmin=true` so the
operator token can administer workspaces. In a multi-user deployment leave it
false and use OIDC.

### PostgreSQL

```bash
PGPASSWORD="$(openssl rand -hex 16)"
helm upgrade --install poddy-postgres oci://registry-1.docker.io/bitnamicharts/postgresql \
  -n poddy-system --create-namespace \
  --set auth.username=poddy \
  --set auth.password="$PGPASSWORD" \
  --set auth.database=poddy

kubectl -n poddy-system create secret generic poddy-database \
  --from-literal=database-url="postgres://poddy:${PGPASSWORD}@poddy-postgres-postgresql:5432/poddy?sslmode=disable"
```

### SSH key

For normal team use, skip this section: after signing in, open **SSH keys** in
the portal and add your public key. For this token-based operator test, generate
an administrator-managed key. Its comment must map it to the workspace owner;
with token auth the owner is `token`, so use exactly `user:token` (a bare
`token` comment is unmapped and denied):

```bash
ssh-keygen -t ed25519 -N '' -f ~/.ssh/poddy-clean -C 'user:token'
kubectl -n poddy-system create secret generic poddy-ssh-keys \
  --from-file=authorized_keys="$HOME/.ssh/poddy-clean.pub"
```

For OIDC deployments, use the user's email or username instead:
`alice@example.com` or `user:alice`. Keys commented `admin` or `*` can access
every workspace.

### SSH host key

A stable host key avoids `known_hosts` warnings after restarts:

```bash
ssh-keygen -t ed25519 -N '' -f /tmp/poddy-clean-host-key
kubectl -n poddy-system create secret generic poddy-host-key \
  --from-file=ssh_host_ed25519_key=/tmp/poddy-clean-host-key
```

## 4. Values file

Save as `k3s-test-values.yaml` and replace `<NODE_IP>` with the k3s node IP
(for example `172.29.74.141`):

```yaml
fullnameOverride: poddy
replicaCount: 1

bastion:
  enabled: true
  replicaCount: 1

image:
  repository: poddy
  tag: clean
  pullPolicy: IfNotPresent

auth:
  existingSecret: poddy-auth
  secretKey: token
  tokenAdmin: true

database:
  existingSecret: poddy-database
  urlKey: database-url
  autoMigrate: true

workspaceNamespace: poddy-workspaces
createWorkspaceNamespace: false

ssh:
  enabled: true
  addr: ":2222"
  publicHost: <NODE_IP>
  publicPort: 30223
  existingKeysSecret: poddy-ssh-keys
  keysSecretKey: authorized_keys
  allowUnmappedKeys: false
  existingHostKeySecret: poddy-host-key
  hostKeySecretKey: ssh_host_ed25519_key
  service:
    type: NodePort
    port: 2222
    nodePort: 30223

http:
  service:
    type: NodePort
    port: 80
    nodePort: 30081

rbac:
  create: true
  createConfigMap: true
  admins: []

defaultTemplate: base

ingress:
  enabled: true
  className: traefik
  hosts:
    - host: poddy.local
      paths:
        - path: /
          pathType: Prefix
```

`ingress` is optional. The NodePort URL works without editing `/etc/hosts`.
To use the ingress host, add `<NODE_IP> poddy.local` to `/etc/hosts`.

## 5. Install

```bash
helm upgrade --install poddy deploy/helm/poddy \
  -n poddy-system -f k3s-test-values.yaml --wait
```

Check the deployment and namespaces:

```bash
kubectl -n poddy-system get deploy,svc,pods
kubectl -n poddy-workspaces get role,rolebinding
```

The Role and RoleBinding must be in `poddy-workspaces`, not `poddy-system`.

## 6. Access the UI

Open `http://<NODE_IP>:30081` and paste the token, or use:

```bash
kubectl -n poddy-system get secret poddy-auth -o jsonpath='{.data.token}' | base64 -d
```

The UI can also be reached at `http://poddy.local` if you added the hosts entry.

## 7. Create and test a workspace

Create a workspace from the UI, or with the API:

```bash
TOKEN="$(kubectl -n poddy-system get secret poddy-auth -o jsonpath='{.data.token}' | base64 -d)"
curl -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -X POST "http://<NODE_IP>:30081/api/workspaces" \
  --data '{"name":"demo","template":"base","parameters":{"cpu":"250m","memory":"256Mi","storage":"1Gi"}}'
```

Wait for the pod:

```bash
kubectl -n poddy-workspaces get pod poddy-demo -w
```

Verify the owner annotation and per-workspace SSH Secret:

```bash
kubectl -n poddy-workspaces get pod poddy-demo \
  -o jsonpath='{.metadata.annotations.poddy\.io/owner}{"\n"}'
kubectl -n poddy-workspaces get secret poddy-demo-ssh-keys \
  -o jsonpath='{.data.authorized_keys}' | base64 -d
```

The administrator-managed key comment should be `user:token`, and the pod must
not contain other users' keys. Self-service keys added from the portal update
this Secret automatically; existing pods pick them up without a restart because
the file is mounted from the Secret.

## 8. SSH into the workspace

The `poddy-bastion` Deployment listens on `30223`. The target host is the
workspace name with the `poddy-` prefix:

```bash
ssh -i ~/.ssh/poddy-clean \
  -o ProxyCommand='ssh -i ~/.ssh/poddy-clean -p 30223 -W %h:%p root@<NODE_IP>' \
  dev@poddy-demo
```

The **VS Code** and **Cursor** buttons open the desktop editor through the
Poddy extension. Install the VSIX once with
`code --install-extension poddy-remote-<version>.vsix` or
`cursor --install-extension poddy-remote-<version>.vsix` (from the latest GitHub
release or a local `npm run package`); the extension enrolls a machine key and
writes a `# >>> poddy:<workspace>` block in the real `~/.ssh/config`. No manual
key copy is needed. The **PyCharm** button opens JetBrains Gateway; enable
**Parse config file ~/.ssh/config** in Gateway. The **SSH** button remains a
manual fallback and produces a `Host poddy-ssh-*` `ProxyCommand` configuration.
Keys mapped to another owner are rejected with
`not authorized for this workspace`; keys not present in the Secret are
rejected with `Permission denied (publickey)`.

### Windows / WSL

Windows VS Code and Cursor use Windows OpenSSH, not WSL. The extension installs
into the Windows editor profile and writes
`C:\Users\<you>\.ssh\config`, so the Remote-SSH window uses the same key. The first connection can take up to a
minute while Kubernetes propagates the workspace Secret. If you use the manual
**SSH** fallback, save that config in `C:\Users\<you>\.ssh\config` and copy
the private key to `C:\Users\<you>\.ssh\`; a config saved only in WSL is
ignored by Windows editors, which report
`Could not resolve hostname poddy-ssh-<name>`.

## 9. Upgrade and cleanup

Upgrade after changing the image or values:

```bash
docker build -t poddy:clean .
docker save poddy:clean | sudo k3s ctr images import -
kubectl -n poddy-system rollout restart deployment/poddy deployment/poddy-bastion
```

Remove the test deployment and its data:

```bash
helm -n poddy-system uninstall poddy
kubectl delete namespace poddy-workspaces poddy-system
```

**Warning**: deleting `poddy-workspaces` deletes all workspace PVCs and data.
For a production namespace, create it outside Helm and set
`createWorkspaceNamespace: false` so `helm uninstall` cannot remove it.

## Security notes

- This test uses the shared token as an administrator via
  `auth.tokenAdmin=true`. Use OIDC for real multi-user deployments.
- Users can add self-service keys under **SSH keys**; those keys are bound to
  their signed-in identity. Administrator-managed keys are scoped by their key
  comment, and unmapped keys are denied.
- Workspaces run in `poddy-workspaces`; the portal cannot access other
  namespaces. For hard tenant isolation, use one release/namespace per team.
- Add a NetworkPolicy if the workspace namespace should not be reachable
  directly inside the cluster.
