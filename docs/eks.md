# Amazon EKS with ALB, EBS, and EFS

This page shows a production-oriented EKS deployment:

- **ALB Ingress** for the portal, with an ACM certificate.
- **NLB Service** for SSH through the bastion.
- **EBS** for each workspace's `/home/dev` home volume.
- **EFS** for shared data at `/shared/<workspace>`.
- **Okta OIDC** for sign-in.

The manifests are in
[`examples/eks/`](https://github.com/SlimaineB/poddy/tree/main/examples/eks).

!!! note "EBS and EFS have different access modes"
    poddy creates the per-workspace PVC as `ReadWriteOnce`, which is correct for
    EBS. EFS access points are `ReadWriteMany`. Use EBS for `/home/dev` and EFS
    for shared data. Do not point the `storageClass` template parameter at an
    EFS StorageClass; the workspace PVC may stay `Pending`.

## Prerequisites

- An EKS cluster with OIDC provider enabled (for IRSA).
- `kubectl`, `helm`, and `eksctl` or equivalent.
- The [AWS Load Balancer Controller](https://kubernetes-sigs.github.io/aws-load-balancer-controller/)
  installed with IRSA.
- The [AWS EBS CSI driver](https://github.com/kubernetes-sigs/aws-ebs-csi-driver)
  installed with IRSA for workspace home volumes.
- The [AWS EFS CSI driver](https://github.com/kubernetes-sigs/aws-efs-csi-driver)
  installed with IRSA for shared data.
- An EFS file system with mount targets in the cluster VPC.
- An ACM certificate for `poddy.example.com` in the same region as the ALB.
- Route53 (or another DNS provider) for `poddy.example.com` and
  `ssh.poddy.example.com`.

## 1. Install the AWS Load Balancer Controller

Create the controller's IRSA service account with the
`AWSLoadBalancerControllerIAMPolicy` described in the AWS documentation, then
install the chart:

```bash
export CLUSTER=poddy
export REGION=eu-west-1

helm repo add eks https://aws.github.io/eks-charts
helm upgrade --install aws-load-balancer-controller eks/aws-load-balancer-controller \
  -n kube-system \
  --set clusterName="$CLUSTER" \
  --set region="$REGION" \
  --set serviceAccount.create=false \
  --set serviceAccount.name=aws-load-balancer-controller
```

The public subnets need the `kubernetes.io/role/elb=1` tag and the cluster's
`kubernetes.io/cluster/<name>=shared` tag. Private subnets need
`kubernetes.io/role/internal-elb=1` if you use an internal ALB.

## 2. Install the EBS CSI driver and create a StorageClass

Create an IRSA service account with the `AmazonEBSCSIDriverPolicy` managed
policy, then install the driver:

```bash
helm repo add aws-ebs-csi-driver https://kubernetes-sigs.github.io/aws-ebs-csi-driver
helm upgrade --install aws-ebs-csi-driver aws-ebs-csi-driver/aws-ebs-csi-driver \
  -n kube-system \
  --set controller.serviceAccount.create=false \
  --set controller.serviceAccount.name=ebs-csi-controller-sa
```

EKS does not always create a default StorageClass. Apply the example `gp3`
class and keep `workspace.storageClass: poddy-gp3` in the values file:

```bash
kubectl apply -f examples/eks/ebs-storageclass.yaml
kubectl get storageclass poddy-gp3
```

Use a different class name in `workspace.storageClass` if your cluster already
has an EBS StorageClass.

## 3. Install the EFS CSI driver

Create an IRSA service account with the `AmazonEFSCSIDriverPolicy` managed
policy, then install the driver:

```bash
helm repo add aws-efs-csi-driver https://kubernetes-sigs.github.io/aws-efs-csi-driver/
helm upgrade --install aws-efs-csi-driver aws-efs-csi-driver/aws-efs-csi-driver \
  -n kube-system \
  --set controller.serviceAccount.create=false \
  --set controller.serviceAccount.name=efs-csi-controller-sa
```

The EFS security group must allow NFS (TCP 2049) from the worker node security
group. The EFS file system must have a mount target in every subnet used by the
nodes, or use a regional file system with mount targets in each AZ.

## 4. Create the EFS StorageClass and shared PVC

Replace `fs-REPLACE_ME` in
[`examples/eks/efs-storageclass.yaml`](https://github.com/SlimaineB/poddy/blob/main/examples/eks/efs-storageclass.yaml)
with your file system ID:

```bash
kubectl create namespace poddy-workspaces

kubectl apply -f examples/eks/efs-storageclass.yaml
kubectl apply -f examples/eks/shared-efs-pvc.yaml
kubectl -n poddy-workspaces get pvc poddy-shared-efs
```

The StorageClass uses EFS access points, `uid`/`gid` `1000` (the `dev` user in
the workspace image), and `basePath: /poddy`. Recent EFS CSI driver versions
support `uid`/`gid`; for older versions use `gidRangeStart`/`gidRangeEnd`
instead.

EFS is elastic. The `5Gi` request on the PVC is a nominal Kubernetes value, not
a hard limit.

## 5. Prepare namespaces and secrets

```bash
kubectl create namespace poddy-system
kubectl create namespace poddy-workspaces
```

Create the portal token, SSH host key, and authorized keys:

```bash
kubectl -n poddy-system create secret generic poddy-auth \
  --from-literal=token="$(openssl rand -hex 32)"

ssh-keygen -t ed25519 -N '' -f poddy_host_ed25519
kubectl -n poddy-system create secret generic poddy-host-key \
  --from-file=ssh_host_ed25519_key=poddy_host_ed25519

# Each key comment binds it to an owner: alice@example.com, user:alice, or admin.
kubectl -n poddy-system create secret generic poddy-ssh-keys \
  --from-file=authorized_keys=authorized_keys
```

Create the Okta OIDC Secret. See [Okta SSO](okta.md) for the Okta application
and groups claim:

```bash
kubectl -n poddy-system create secret generic poddy-oidc \
  --from-literal=oidc-client-id="$OKTA_CLIENT_ID" \
  --from-literal=oidc-client-secret="$OKTA_CLIENT_SECRET" \
  --from-literal=oidc-session-key="$(openssl rand -hex 32)"
```

## 6. Install poddy

Copy `examples/eks/values.yaml` and replace:

- the ACM certificate ARN;
- the Okta issuer, allowed groups, and optional `oidc.discoveryUrl` /
  `oidc.authorizationUrl` if your provider uses internal endpoints;
- the public hostnames;
- `workspace.storageClass` if you use a different EBS class than `poddy-gp3`;
- `workspace.imageRegistry`, `workspace.imageRegistryMap`, and
  `workspace.imagePullSecrets` if you mirror workspace images through a
  private registry;
- the image tag, if you pin a release.

Then install:

```bash
helm upgrade --install poddy oci://ghcr.io/slimaineb/charts/poddy \
  -n poddy-system --create-namespace --version 0.1.5 \
  -f examples/eks/values.yaml
```

The chart creates:

- an `Ingress` with `ingressClassName: alb`;
- a `Service` of type `LoadBalancer` for SSH, annotated for the AWS Load
  Balancer Controller;
- the portal and bastion Deployments.

Apply the EFS workspace template in the workspace namespace:

```bash
kubectl apply -f examples/eks/efs-workspace-template.yaml
```

The portal watches labeled template ConfigMaps and reloads within a few seconds.
The new **EFS shared** template appears in the create dialog. Poddy's defaults
add the `code-server` sidecar when `browserVSCode` is `"true"`, so workspaces
also show **VS Code Web**; set the template parameter `browserVSCode` to
`false` to omit it.

## 7. Point DNS at the load balancers

Get the ALB hostname and create a Route53 alias for `poddy.example.com`:

```bash
kubectl -n poddy-system get ingress poddy \
  -o jsonpath='{.status.loadBalancer.ingress[0].hostname}'
```

Get the NLB hostname and create a Route53 alias for `ssh.poddy.example.com`:

```bash
kubectl -n poddy-system get svc poddy-ssh \
  -o jsonpath='{.status.loadBalancer.ingress[0].hostname}'
```

If you use different hostnames, update `ingress.hosts`, `oidc.redirectUrl`, and
`ssh.publicHost` in the values file and run `helm upgrade` again. The Okta
application's sign-in redirect URI must match `oidc.redirectUrl` exactly.

The ALB can take a few minutes to become active. The NLB is created by the AWS
Load Balancer Controller because the Service uses
`service.beta.kubernetes.io/aws-load-balancer-type: external`.

## 8. Create a workspace with EFS

1. Open `https://poddy.example.com` and sign in through Okta.
2. Create a workspace and choose the **EFS shared** template.
3. The workspace gets:
   - `/home/dev` on a per-workspace EBS volume;
   - `/shared/<workspace>` on the shared EFS PVC.
4. Verify the resources:

```bash
kubectl -n poddy-workspaces get pods,pvc
kubectl -n poddy-workspaces describe pod poddy-<workspace>
```

The init container creates `/shared/<workspace>` on EFS. The workspace container
mounts that subdirectory at `/shared`, so workspaces do not overwrite each
other's files.

## Using EFS as the home volume

If you want `/home/dev` itself on EFS, you need a `ReadWriteMany` PVC. poddy's
built-in PVC creation is `ReadWriteOnce`. Two options:

- Pre-create a static PV/PVC named `poddy-<workspace>` with the access modes you
  need, then set the template `storage` parameter. poddy reuses an existing PVC
  with that name.
- Change poddy's PVC access mode in code if all workspaces should use EFS.

For most teams, EBS for `/home/dev` plus EFS for `/shared` is simpler and
faster.

## PostgreSQL on EFS

If you run PostgreSQL on EFS, set `PGDATA` to a subdirectory such as
`/var/lib/postgresql/data/pgdata`. EFS adds `lost+found` at the volume root, and
PostgreSQL refuses to initialize a non-empty data directory. See
[PostgreSQL on EFS](postgres-schema.md#postgresql-on-efs).

## Troubleshooting

| Symptom | Check |
|---------|-------|
| ALB is not created | AWS Load Balancer Controller logs, IRSA annotations, public subnet tags, and the ACM certificate ARN. |
| Ingress returns 503 | The target group has no healthy targets. Check `alb.ingress.kubernetes.io/healthcheck-path: /healthz` and the portal Service endpoints. |
| NLB is not created | Controller logs, `aws-load-balancer-type: external`, subnet tags, and the SSH Service annotations. |
| SSH connects but the host key changes | Set `ssh.existingHostKeySecret`; all bastion replicas must share the same host key. |
| Workspace PVC stays `Pending` | `workspace.storageClass` is empty and the cluster has no default, or the EBS CSI driver is missing. |
| `poddy-shared-efs` stays `Pending` | The EFS CSI controller, StorageClass name, file system ID, and EFS mount targets. |
| Workspace pod stuck mounting EFS | EFS security group allows TCP 2049 from the node security group; mount targets exist in the node AZs. |
| `Permission denied` on `/shared` | The StorageClass `uid`/`gid` must match the workspace user (1000), or use the older `gidRangeStart`/`gidRangeEnd` parameters. |
| Okta login fails | Issuer, redirect URI, client secret, and groups claim. See [Okta SSO](okta.md). |
| `invalid_scope` from Okta | Remove `groups` from `oidc.scopes` unless the authorization server defines that scope. |

## Related

- [Production deployment](deploy.md) — generic Helm values and secrets.
- [Okta SSO](okta.md) — Okta application and groups claim.
- [Workspace templates](templates.md) — template parameters and pod YAML.
- [RBAC and ownership](rbac.md) — admins, teams, and workspace ownership.
