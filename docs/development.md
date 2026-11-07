# Development

This page is for contributors. If you just want to run poddy, use
[Getting started](getting-started.md) or [Kubernetes deployment](deploy.md).
The published GHCR images are built by the publish workflow; you do not need to
build or push anything for a normal install.

## Before you start

You need:

- Go 1.26+
- Docker
- `kubectl`
- A running Kubernetes or k3s cluster

## Build and run the portal

```bash
go build -o poddy .
```

The local binary runs the portal and the SSH bastion in one process:

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

The web UI is at <http://127.0.0.1:8090>. The standalone cluster bastion is
built from `cmd/poddy-bastion` and runs as a separate Deployment in Helm.

## Build workspace images locally

The built-in templates point at the published GHCR images. For local image
development, build and import your own tags into k3s:

```bash
docker build -f Dockerfile.workspace-base -t poddy/workspace-base:latest .
docker build -f Dockerfile.workspace-go -t poddy/workspace:latest .
docker build --build-arg BASE_IMAGE=poddy/workspace-base:latest -f Dockerfile.workspace-node -t poddy/workspace-node:latest .
docker build --build-arg BASE_IMAGE=poddy/workspace-base:latest -f Dockerfile.workspace-python -t poddy/workspace-python:latest .

docker save poddy/workspace-base:latest poddy/workspace:latest \
  poddy/workspace-node:latest poddy/workspace-python:latest \
  | sudo k3s ctr images import -
```

Then override the `image` field in your workspace template so pods use the
local tags. See [Workspace templates](templates.md).

## Build the portal image

```bash
docker build -t poddy:dev .
docker save poddy:dev | sudo k3s ctr images import -
```

Use `image.repository=poddy` and `image.tag=dev` with `image.pullPolicy=IfNotPresent`
for a local Helm install.

## Publishing images

The GitHub Actions publish workflow builds and pushes the portal/bastion image,
the workspace base image, and the Go, Node.js, and Python workspace images to
GHCR on pushes to `main` and `v*` tags. Manual `docker push` is not part of the
user setup flow.
