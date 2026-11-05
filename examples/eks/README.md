# Amazon EKS example

This directory contains a production-oriented EKS example:

| File | Purpose |
|------|---------|
| `values.yaml` | Helm values for ALB Ingress, NLB SSH, Okta OIDC, and two replicas. |
| `efs-storageclass.yaml` | EFS CSI dynamic-provisioning StorageClass. |
| `shared-efs-pvc.yaml` | Shared `ReadWriteMany` PVC named `poddy-shared-efs`. |
| `efs-workspace-template.yaml` | Workspace template with an EBS home, shared EFS at `/shared`, and an optional browser VS Code sidecar. |

The example uses EBS for the per-workspace `/home/dev` volume and EFS for
shared data. poddy creates the workspace PVC as `ReadWriteOnce`, which is the
right mode for EBS; EFS access points are `ReadWriteMany`.

See [`docs/eks.md`](../../docs/eks.md) for the full walkthrough: controllers,
IRSA, EFS, secrets, install, DNS, and troubleshooting.
