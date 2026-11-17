---
title: "Local k3d and kind clusters"
description: "Validate the same Poddy Helm charts on k3d (k3s in Docker) and kind (upstream Kubernetes in Docker)."
---

k3d and kind both run a Kubernetes cluster inside Docker. Poddy itself should
behave the same on both: the Helm charts, container images, API, workspace
StatefulSets, PVC requests, and SSH path are identical. The differences are in
the cluster distribution and in how images and ports are exposed.

This page describes the validation used for the repository and the expected
differences. The automated harness is
[`scripts/validate-k3d-kind.sh`](https://github.com/SlimaineB/poddy/blob/main/scripts/validate-k3d-kind.sh).

## Quick path

The Makefile wraps cluster creation, local image build, image loading,
deployment, and validation:

```bash
make k3d-deploy
make kind-deploy
make local-deploy   # both clusters
make local-down     # delete both clusters
```

`make k3d-deploy` builds the portal image as `poddy:local` with Docker, creates
the k3d cluster, imports the portal, workspace base, and PostgreSQL images,
then runs the validation harness. `make kind-deploy` does the same with kind.
Override `LOCAL_IMAGE_TAG`, `K3D_CLUSTER`, `KIND_CLUSTER`, or
`KIND_NODE_IMAGE` if needed.

The remaining sections show the equivalent commands manually.

## Tested versions

| Component | k3d | kind |
|-----------|-----|------|
| Cluster tool | k3d v5.9.0 | kind v0.33.0 |
| Kubernetes | v1.35.5+k3s1 | v1.35.0 |
| Runtime | containerd 2.2.3-k3s1 | containerd 2.2.0 |
| Node OS | K3s | Debian 12 bookworm |
| Default StorageClass | `local-path` | `standard` |
| Image loading | `k3d image import` | `kind load docker-image` |

Use a pinned kind node image for reproducible tests. With kind v0.33.0, the
default `kindest/node:v1.37.0` did not start on WSL2 kernel 6.18 and failed
with:

```text
could not find a log line that matches "Reached target .*Multi-User System.*|detected cgroup v1"
```

`kindest/node:v1.35.0` started normally and matches the Kubernetes version used
by the k3d test closely enough for application validation.

## 1. Create the clusters

k3d:

```bash
k3d cluster create poddy-k3d-test \
  --wait \
  --kubeconfig-update-default=false \
  --kubeconfig-switch-context=false
k3d kubeconfig get poddy-k3d-test > /tmp/poddy-k3d.kubeconfig
```

kind:

```bash
kind create cluster \
  --name poddy-kind-test \
  --image kindest/node:v1.35.0 \
  --kubeconfig /tmp/poddy-kind.kubeconfig \
  --wait 300s
```

k3d installs a default `local-path` StorageClass. kind installs a default
`standard` StorageClass backed by the local-path provisioner. Poddy workspace
templates leave `storageClass` empty and therefore use the cluster default on
both distributions.

## 2. Preload images

Preloading avoids registry rate limits and removes image pull time from the
comparison:

```bash
docker pull ghcr.io/slimaineb/poddy:latest
docker pull ghcr.io/slimaineb/poddy-workspace-base:latest
docker pull postgres:16-alpine

k3d image import -c poddy-k3d-test \
  ghcr.io/slimaineb/poddy:latest \
  ghcr.io/slimaineb/poddy-workspace-base:latest \
  postgres:16-alpine

kind load docker-image --name poddy-kind-test \
  ghcr.io/slimaineb/poddy:latest \
  ghcr.io/slimaineb/poddy-workspace-base:latest \
  postgres:16-alpine
```

## 3. Run the validation

The harness uses the charts in the current checkout, installs PostgreSQL,
creates the `poddy-system` and `poddy-workspaces` namespaces, enables token
admin, and creates a `base` workspace with `250m` CPU, `512Mi` memory, and a
`500Mi` PVC. It then verifies:

- `GET /healthz` on the portal.
- The workspace reaches the `Running` phase through the API.
- The workspace PVC reaches `Bound`.
- SSH through the bastion succeeds and lands in `/home/dev` as `dev`.

It deliberately sets `ssh.agent.enabled=false` and `ssh.requireAgent=false`, so
the test exercises the bastion-to-workspace port-forward path. The agent
sidecar path is covered separately.

```bash
scripts/validate-k3d-kind.sh k3d /tmp/poddy-k3d.kubeconfig 18080 12222
scripts/validate-k3d-kind.sh kind /tmp/poddy-kind.kubeconfig 18081 12223
```

Expected result on both clusters:

```text
portal healthz: ok
workspace phase: Running
poddy-<workspace>   Bound   ...   500Mi   RWO   <cluster-default>
SSH_OK user=dev cwd=/home/dev
PASS
```

## 4. Clean up

```bash
k3d cluster delete poddy-k3d-test
kind delete cluster --name poddy-kind-test
```

## Distribution differences

| Behavior | k3d | kind | Impact on Poddy |
|----------|-----|------|-----------------|
| Kubernetes distribution | k3s | upstream Kubernetes | None observed |
| Default storage | `local-path` | `standard` (`rancher.io/local-path`) | Workspaces use the default; do not hardcode `local-path` |
| LoadBalancer | k3d servicelb | None by default | Use `ClusterIP` plus port-forward, or configure port mappings |
| NodePort from host | Exposed through the k3d load balancer | Requires `extraPortMappings` | Port-forward is portable across both |
| Image load command | `k3d image import` | `kind load docker-image` | Same images and digests |
| SSH host key/window | Same chart behavior | Same chart behavior | Identical |

The application-level results matched: same portal image, same workspace image,
same `500Mi` PVC request, same workspace phase transition, and the same SSH
result. Only the StorageClass names reported by the respective clusters
differed.

## Portability finding

The demo PostgreSQL manifest used to hardcode `storageClassName: local-path`.
That worked on k3s/k3d but left the PVC `Pending` on kind, whose default class is
`standard`:

```text
Warning ProvisioningFailed storageclass.storage.k8s.io "local-path" not found
```

The demo manifest now omits `storageClassName`, so PostgreSQL uses the default
StorageClass on both clusters. This is the correct behavior for a portable demo:
if a deployment needs a specific class, expose it as a Helm value instead of
hardcoding a distribution-specific name.

## Troubleshooting

### Workspace stays `Pending`

Check the PVC and its StorageClass:

```bash
kubectl --kubeconfig <kubeconfig> -n poddy-workspaces get pvc,pods
kubectl --kubeconfig <kubeconfig> get storageclass
```

If the PVC references `local-path` on kind, create a `local-path` class or fix
the manifest to use the cluster default.

### Kind node never becomes Ready

Pin a known-good node image, for example
`--image kindest/node:v1.35.0`, and recreate the cluster. Delete the failed
cluster first:

```bash
kind delete cluster --name poddy-kind-test
```

### NodePort unreachable from the host

Do not rely on NodePort on kind without `extraPortMappings`. Use port-forward
for the portal and SSH services, as the validation harness does.
