# Clean k3s test deployment

This guide installs a test deployment with the control plane and workspaces in
**separate namespaces**. It uses the shared token for login (no OIDC), a stable
SSH host key, and NodePort services so it works on a local k3s cluster without
a cloud load balancer. The example also enables an optional Traefik ingress.

## Layout

| Component | Namespace | Notes |
|-----------|-----------|-------|
| Portal + SSH bastion | `poddy-system` | Helm release `poddy` |
| Workspace pods + PVCs | `poddy-workspaces` | Dedicated namespace |
| Templates | `poddy-workspaces` | Built-in `base`/`go` templates, or the `poddy-templates` chart |

The portal's Kubernetes Role is created in `poddy-workspaces`; the
ServiceAccount stays in `poddy-system`. The portal cannot access other
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

The token identity is `token`. It is a global admin in token mode.

### SSH key

Generate a key for this deployment. The key comment must map the key to the
workspace owner. With token auth the owner is `token`, so use exactly
`user:token` (a bare `token` comment is unmapped and denied):

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

image:
  repository: poddy
  tag: clean
  pullPolicy: IfNotPresent

auth:
  existingSecret: poddy-auth
  secretKey: token

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

Verify the owner annotation and injected keys:

```bash
kubectl -n poddy-workspaces get pod poddy-demo \
  -o jsonpath='{.metadata.annotations.poddy\.io/owner}{"\n"}'
kubectl -n poddy-workspaces exec poddy-demo -- \
  sh -lc 'printf "%s\n" "$SSH_AUTHORIZED_KEY"'
```

The key comment should be `user:token`; the pod should not contain other users'
keys. If you change the key comment later, update the Secret, restart the portal
(`kubectl -n poddy-system rollout restart deployment/poddy`), then stop/start
existing workspaces to refresh their injected keys.

## 8. SSH into the workspace

The bastion listens on `30223`. The target host is the workspace name with the
`poddy-` prefix:

```bash
ssh -i ~/.ssh/poddy-clean \
  -o ProxyCommand='ssh -i ~/.ssh/poddy-clean -p 30223 -W %h:%p root@<NODE_IP>' \
  dev@poddy-demo
```

The UI's **SSH** button produces a `Host poddy-ssh-*` `ProxyJump`
configuration that covers all workspaces. After you install it once, the
**VS Code** button opens Remote-SSH with the
`vscode://vscode-remote/ssh-remote+...` URL. Keys mapped to another owner are
rejected with `not authorized for this workspace`; keys not present in the
Secret are rejected with `Permission denied (publickey)`.

### Windows / WSL

Windows VS Code uses Windows OpenSSH, not WSL. Save the config from the **SSH**
dialog in `C:\Users\<you>\.ssh\config` and copy the private key to
`C:\Users\<you>\.ssh\`. The `Host poddy-ssh-*` block covers all workspaces,
so new workspaces do not need another config entry. A config saved only in WSL
is ignored by Windows VS Code, which then reports
`Could not resolve hostname poddy-ssh-<name>`. The generated config accepts the
bastion host key on first use and skips host-key checking for the ephemeral
workspace pod.

## 9. Upgrade and cleanup

Upgrade after changing the image or values:

```bash
docker build -t poddy:clean .
docker save poddy:clean | sudo k3s ctr images import -
kubectl -n poddy-system rollout restart deployment/poddy
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

- This test uses the shared token, which is a global admin. Use OIDC for real
  multi-user deployments.
- SSH keys are scoped to their owner by the key comment. Unmapped keys are
  denied. Admin keys (`admin`, `*`) are break-glass and should be protected.
- Workspaces run in `poddy-workspaces`; the portal cannot access other
  namespaces. For hard tenant isolation, use one release/namespace per team.
- Add a NetworkPolicy if the workspace namespace should not be reachable
  directly inside the cluster.
