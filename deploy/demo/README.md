# Full-experience demo

Deploys poddy on k3s with everything needed to demo the external-auth and
volume backup/restore flows:

- **Dex** for portal OIDC login (NodePort `30556`)
- **PostgreSQL** for metadata and external-auth token storage
- **MinIO** (S3-compatible) for workspace volume backups (NodePort `30901` console)
- **Gitea** for external OAuth2 credentials (NodePort `30300`)
- poddy portal (NodePort `30081`) with a `gitea` workspace template

This is a demo environment. It uses known passwords, `auth.tokenAdmin=true`,
and local-path storage. Do not expose it as-is.

## Run

```bash
./deploy/demo/demo.sh
```

The script builds the portal image, imports it into k3s, creates the required
Secrets, applies Dex/PostgreSQL/MinIO/Gitea, creates a Gitea admin and OAuth2
application, and installs the Helm releases.

Useful overrides:

```bash
DEMO_HOST=192.168.1.50 ./deploy/demo/demo.sh
IMAGE_TAG=demo-8268d99 SKIP_BUILD=1 ./deploy/demo/demo.sh
PODDY_KUBECONFIG=/path/to/kubeconfig ./deploy/demo/demo.sh
```

`SKIP_BUILD=1` assumes `ghcr.io/slimaineb/poddy:$IMAGE_TAG` is already imported
into the k3s containerd image store.

## Verify

```bash
./deploy/demo/verify.py
```

The verification script:

1. Starts the portal-to-Gitea OAuth2 flow and signs in to Gitea.
2. Authorizes the poddy OAuth2 application and completes the callback.
3. Checks that the portal reports `gitea` as authenticated.
4. Creates a workspace from the `gitea` template and checks that
   `GITEA_TOKEN` and `PODDY_EXTERNAL_AUTH_GITEA_TOKEN` are injected.
5. Uses the workspace token to fetch a refreshed Gitea token from the portal.
6. Writes a proof file, stops the workspace, backs it up to MinIO, deletes the
   file, restores the backup, and checks the file content.
7. Deletes the verification workspace unless `KEEP_DEMO_WORKSPACE=1` is set.

## Credentials

| Service | URL | Credentials |
|---------|-----|-------------|
| poddy | `http://<host>:30081` | Dex users below, or the shared token in `/tmp/opencode/poddy-demo.env` |
| Dex | `http://<host>:30556/dex` | `admin@example.com` / `admin`, `dev@example.com` / `poddy`, `alice@example.com` / `alice`, `ops@example.com` / `ops`, `bob@example.com` / `bob`, `carol@example.com` / `carol`, `dave@example.com` / `dave`, `erin@example.com` / `erin` |
| Gitea | `http://<host>:30300` | `poddy` / `poddy-demo` |
| MinIO console | `http://<host>:30901` | `minioadmin` / `minioadmin` |

The generated environment file is `/tmp/opencode/poddy-demo.env`.

## Files

| File | Purpose |
|------|---------|
| `demo.sh` | Build, deploy, and configure the stack |
| `verify.py` | End-to-end proof of external auth and backup/restore |
| `values.yaml` | poddy Helm values with OIDC, backup, and external auth |
| `templates-values.yaml` | Adds the `gitea` workspace template |
| `dex.yaml` | Dex config and Deployment/Service |
| `postgres.yaml` | PostgreSQL StatefulSet/Service |
| `minio.yaml` | MinIO Deployment/Service and PVC |
| `gitea.yaml` | Gitea Deployment/Service and PVC |

## Cleanup

```bash
helm -n poddy-system uninstall poddy
helm -n poddy-workspaces uninstall poddy-templates
kubectl delete -f deploy/demo/gitea.yaml -f deploy/demo/minio.yaml \
  -f deploy/demo/postgres.yaml -f deploy/demo/dex.yaml
kubectl delete pvc -n poddy-system minio-data gitea-data data-poddy-postgres-0
```

The demo keeps MinIO backups on the `minio-data` PVC. Remove that PVC to delete
them.
