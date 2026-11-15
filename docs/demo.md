# Full product demo

The demo deploys poddy on a local k3s cluster with the supporting services
needed to show the parts a quick start cannot:

- **Dex** for portal OIDC login;
- **PostgreSQL** for control-plane state;
- **MinIO** for workspace volume backups;
- **Gitea** for external OAuth2 credentials and a `gitea` workspace template;
- poddy with NodePorts and a complete RBAC/template example.

!!! danger "Demo only"
    The demo uses known passwords, `auth.tokenAdmin=true`, local-path storage,
    and plain HTTP. It is not a production blueprint. Do not expose it to the
    internet or reuse its credentials.

## What the demo proves

The demo also configures Dex OIDC so you can test the browser sign-in flow
manually. The included verification script focuses on the paths that are easy to
get wrong in a real deployment:

1. GitHub-style OAuth2 connection to Gitea.
2. Workspace creation from a template that requires external auth.
3. Injection of `GITEA_TOKEN` and `PODDY_EXTERNAL_AUTH_GITEA_TOKEN`.
4. Refreshing the external token from inside the workspace.
5. The workspace CLI (`poddy external-auth`, `poddy git-askpass`).
6. Stopping a workspace, backing up its PVC to MinIO, deleting a file,
   restoring the backup, and reading the file back.

It prints `PROOF OK` only when all of those steps pass.

## Requirements

| Tool | Purpose |
|------|---------|
| k3s or another local Kubernetes cluster | Runs the demo. |
| Docker or a compatible builder | Builds the portal image. |
| `kubectl`, `helm` | Deploy the stack. |
| `make`, `git`, `openssl`, `ssh-keygen` | Build and create demo secrets. |
| `curl`, `jq` | Configure Gitea and wait for services. |
| Python 3 with `requests` and `beautifulsoup4` | Run `verify.py`. |

The script expects a k3s containerd image store for
`k3s ctr images import`. For another cluster, preload the image into its
registry and set `SKIP_BUILD=1`.

## Run the demo

From the repository root:

```bash
./deploy/demo/demo.sh
```

The script:

1. builds `ghcr.io/slimaineb/poddy:demo-<git-sha>` and imports it into k3s;
2. creates the namespaces and the database, auth, OIDC, SSH CA, and
   external-auth secrets;
3. applies Dex, PostgreSQL, MinIO, and Gitea;
4. creates the Gitea admin and the poddy OAuth2 application;
5. installs the templates and portal charts with the demo values.

The first run can take several minutes while images pull.

### Common overrides

```bash
DEMO_HOST=192.168.1.50 ./deploy/demo/demo.sh
PODDY_KUBECONFIG=/path/to/kubeconfig ./deploy/demo/demo.sh
IMAGE_TAG=demo-8268d99 SKIP_BUILD=1 ./deploy/demo/demo.sh
```

`SKIP_BUILD=1` assumes `ghcr.io/slimaineb/poddy:$IMAGE_TAG` is already available
to the cluster.

The default host is the first node's `InternalIP`. The generated environment
file is `/tmp/opencode/poddy-demo.env`.

## Open the demo

Replace `<host>` with the value printed by the script.

| Service | URL | Credentials |
|---------|-----|-------------|
| poddy | `http://<host>:30081` | Dex users below, or the shared token in `/tmp/opencode/poddy-demo.env` |
| Dex | `http://<host>:30556/dex` | `admin@example.com` / `admin` |
| Gitea | `http://<host>:30300` | `poddy` / `poddy-demo` |
| MinIO console | `http://<host>:30901` | `minioadmin` / `minioadmin` |

Dex also includes `dev@example.com` / `poddy`, `alice@example.com` / `alice`,
`ops@example.com` / `ops`, and `bob`, `carol`, `dave`, `erin` with the same
password as their username.

Demo roles:

- `admin@example.com` and `ops@example.com` are global admins.
- `dev@example.com` owns the `gitea` template.
- `alice` and `bob` are in `backend`; `carol` is in `frontend`.
- `gitea` template access is granted to all demo users.
- `go` template access is granted to `dave` and owned by `dev`.

## Run the verification

```bash
./deploy/demo/verify.py
```

Overrides:

```bash
DEMO_ENV=/path/to/poddy-demo.env \
PODDY_KUBECONFIG=/path/to/kubeconfig \
DEMO_WORKSPACE=demo-ext \
KEEP_DEMO_WORKSPACE=1 \
./deploy/demo/verify.py
```

`KEEP_DEMO_WORKSPACE=1` leaves the verification workspace in place so you can
inspect it. Otherwise the script deletes the workspace and its volume.

## Walk through it manually

1. Open the portal and choose **Sign in with Dex**.
2. Sign in as `alice@example.com` / `alice`.
3. Open **Connections** and connect **Gitea**. Authorize the poddy app.
4. Create a workspace from the **gitea** template.
5. When it is **Running**, open the browser terminal and check:

   ```bash
   env | grep GITEA
   poddy external-auth status gitea
   poddy external-auth token gitea
   ```

6. Write a file in `/home/dev`, stop the workspace, open **Volumes**, and back
   up the volume.
7. Start the workspace, delete the file, stop it again, and restore the backup.
8. Start it and confirm the file is back.

## Cleanup

```bash
helm -n poddy-system uninstall poddy
helm -n poddy-workspaces uninstall poddy-templates

kubectl delete -f deploy/demo/gitea.yaml \
  -f deploy/demo/minio.yaml \
  -f deploy/demo/postgres.yaml \
  -f deploy/demo/dex.yaml

kubectl delete pvc -n poddy-system minio-data gitea-data data-poddy-postgres-0
```

The MinIO backups live on the `minio-data` PVC. Delete that PVC if you also want
to remove the backup objects. Delete the `poddy-system` and `poddy-workspaces`
namespaces to remove everything else.

## Demo files

| File | Purpose |
|------|---------|
| `deploy/demo/demo.sh` | Build, deploy, and configure the stack. |
| `deploy/demo/verify.py` | End-to-end proof of external auth and backup/restore. |
| `deploy/demo/values.yaml` | Portal values with OIDC, backup, and external auth. |
| `deploy/demo/templates-values.yaml` | Adds the `gitea` template. |
| `deploy/demo/dex.yaml` | Dex configuration and Deployment. |
| `deploy/demo/postgres.yaml` | Demo PostgreSQL StatefulSet. |
| `deploy/demo/minio.yaml` | MinIO Deployment, Service, and PVC. |
| `deploy/demo/gitea.yaml` | Gitea Deployment, Service, and PVC. |

## Troubleshooting

| Symptom | Check |
|---------|-------|
| Gitea is not reachable | `kubectl -n poddy-system get deploy gitea`; wait for the rollout and check the NodePort firewall. |
| Dex login redirects to the wrong host | Re-run with the correct `DEMO_HOST`; the issuer and callback URLs are rendered at install time. |
| `verify.py` cannot find the provider | The OAuth2 app or external-auth Secret was not created; inspect the script output and `kubectl -n poddy-system get secret poddy-external-auth-config`. |
| Workspace cannot pull the image | Import the image into the cluster or set `IMAGE_TAG` to an available tag. |
| Backup returns 501 | MinIO, `poddy-backup`, or `backup.enabled` is misconfigured. |
| Restore fails | The workspace must be **Stopped**; check the backup key and MinIO credentials. |

See [Troubleshooting](troubleshooting.md) and the
[demo README](https://github.com/slimaineb/poddy/tree/main/deploy/demo).
