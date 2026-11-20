---
title: "Development"
description: "Build poddy from source, run the portal locally, execute tests, and publish images and charts."
---

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
- PostgreSQL 13+ reachable from the portal

## Build and run the portal

```bash
go build -o poddy .
```

The local binary runs the portal, the agent broker, and SSH in one process:

```bash
export KUBE_CONTEXT="$(kubectl config current-context)"
export PODDY_DATABASE_URL='postgres://poddy:poddy@127.0.0.1:5432/poddy?sslmode=disable'

./poddy \
  -context "$KUBE_CONTEXT" \
  -namespace poddy \
  -token=dev-token \
  -ssh-addr=127.0.0.1:2222 \
  -ssh-public-host=127.0.0.1 \
  -ssh-public-port=2222
```

The web UI is at <http://127.0.0.1:8090>. In this mode the portal process also
serves SSH and the workspace agent broker.

## Build workspace images locally

The built-in templates point at the published GHCR images. For local image
development, build and import your own tags into k3s:

```bash
docker build -f docker/workspace/base/Dockerfile -t poddy/workspace-base:latest .
docker build --build-arg BASE_IMAGE=poddy/workspace-base:latest -f docker/workspace/go/Dockerfile -t poddy/workspace:latest .
docker build --build-arg BASE_IMAGE=poddy/workspace-base:latest -f docker/workspace/node/Dockerfile -t poddy/workspace-node:latest .
docker build --build-arg BASE_IMAGE=poddy/workspace-base:latest -f docker/workspace/python/Dockerfile -t poddy/workspace-python:latest .

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

## Tests

Run the Go test suite with `go test ./...`.

The portal UI has a Playwright suite that serves the static files from `web/`
and mocks the API, so it does not need Kubernetes or PostgreSQL:

```bash
cd ui-tests
npm ci
npx playwright install chromium
npm test
```

The same suite runs in CI and through `make ui-test` after a one-time
`make ui-test-install`.

## Publishing images

The GitHub Actions publish workflow builds and pushes the portal image,
the workspace base image, and the Go, Node.js, and Python workspace images to
GHCR on pushes to `main` and `v*` tags. Manual `docker push` is not part of the
user setup flow.
