# Volumes and backups

The **Volumes** page lists persistent volume claims (PVCs) in the workspace
namespace, including volumes that are still attached to a workspace and
orphaned volumes left behind after a workspace is deleted. On a multi-region
deployment, the central hub aggregates the list from every region.

## What the page shows

- Total, assigned, and unassigned volumes, with the sum of requested storage.
- Volume name, workspace, owner, region, PVC phase, workspace phase, capacity,
  and storage class.
- Search and **All / Assigned / Unassigned** filters.
- The region filter from the top bar applies to the volume list.
- The workspace details dialog also lists the PVCs attached to that workspace.

Access follows the normal RBAC rules:

- Admins see every volume and can delete orphaned volumes.
- Regular users see only volumes assigned to their own workspaces.
- Unassigned volumes are visible only to admins.
- A volume that is still assigned to a workspace cannot be deleted. Delete the
  workspace first if the volume should become an orphan.

Poddy only lists PVCs labeled `app.kubernetes.io/name=poddy`, which is how
workspace volumes are created. Manually created PVCs are not shown.

## Requirements

Backups and restores require:

- An S3-compatible bucket. Poddy does not create the bucket.
- Credentials with permission to list the configured prefix and to get, put,
  and delete objects under it.
- Network access from the region manager to the S3 endpoint.
- The volume to be `Bound`.
- Restore requires the target workspace to be **Stopped**. A live restore would
  overwrite files under the running application.
- Backup can run while the workspace is **Running**. The helper is pinned to the
  workspace node and mounts the volume read-only, so a `ReadWriteOnce` volume
  can be read while the workspace uses it. The archive is crash-consistent at
  best: files being written during the backup can be inconsistent. Quiesce the
  application or stop the workspace if you need a fully consistent archive.
- Live backup is not supported for `ReadWriteOncePod` volumes; stop the
  workspace first.

Poddy runs a short-lived helper pod for each backup or restore. The helper
image must contain `sh`, `tar`, and `gzip`; the default `busybox:1.36` is
sufficient. The helper mounts the PVC, runs as root with only the
`CHOWN`, `DAC_OVERRIDE`, `DAC_READ_SEARCH`, and `FOWNER` capabilities, and does
not receive a service account token. The portal Role already allows creating
and deleting pods and exec'ing into them.

## Enable backups with Helm

```yaml
backup:
  enabled: true
  helperImage: busybox:1.36
  s3:
    # A scheme is optional. When present it controls useSSL.
    endpoint: https://minio.example.com:9000
    region: us-east-1
    bucket: poddy-backups
    prefix: poddy-backups
    useSSL: true

    # Preferred in production. The Secret must exist in the release
    # namespace and contain the two keys below.
    existingSecret: poddy-backup
    accessKeyKey: access-key
    secretKeyKey: secret-key

    # Inline credentials are used only when existingSecret is empty.
    # The chart then creates a Secret named <release>-backup.
    accessKey: ""
    secretKey: ""
```

Example Secret:

```bash
kubectl create secret generic poddy-backup -n poddy-system \
  --from-literal=access-key='...' \
  --from-literal=secret-key='...'
```

With an AWS S3 bucket, use an endpoint such as
`s3.us-east-1.amazonaws.com` and set `region` to the bucket region. For MinIO,
use the MinIO service or ingress endpoint, for example
`minio.minio.svc.cluster.local:9000` with `useSSL: false` for in-cluster HTTP.

A minimal bucket policy for a shared bucket looks like this (adapt the bucket
and prefix):

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:ListBucket"],
      "Resource": ["arn:aws:s3:::poddy-backups"],
      "Condition": {
        "StringLike": {"s3:prefix": ["poddy-backups/*"]}
      }
    },
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"],
      "Resource": ["arn:aws:s3:::poddy-backups/poddy-backups/*"]
    }
  ]
}
```

The same values are available as environment variables or flags:

| Helm value | Environment | Flag |
| --- | --- | --- |
| `backup.enabled` | — | — |
| `backup.s3.endpoint` | `PODDY_BACKUP_S3_ENDPOINT` | `-backup-s3-endpoint` |
| `backup.s3.region` | `PODDY_BACKUP_S3_REGION` | `-backup-s3-region` |
| `backup.s3.bucket` | `PODDY_BACKUP_S3_BUCKET` | `-backup-s3-bucket` |
| `backup.s3.prefix` | `PODDY_BACKUP_S3_PREFIX` | `-backup-s3-prefix` |
| `backup.s3.useSSL` | `PODDY_BACKUP_S3_USE_SSL` | `-backup-s3-use-ssl` |
| `backup.s3.accessKey` | `PODDY_BACKUP_S3_ACCESS_KEY` | `-backup-s3-access-key` |
| `backup.s3.secretKey` | `PODDY_BACKUP_S3_SECRET_KEY` | `-backup-s3-secret-key` |
| `backup.helperImage` | `PODDY_BACKUP_HELPER_IMAGE` | `-backup-helper-image` |

When the configuration is valid, the portal logs:

```text
volume backups enabled (S3 bucket poddy-backups)
```

If backup settings are missing or incomplete, the Volumes page still works but
backup actions return `501 volume backups are not configured`.

## Multi-region

Configure the bucket on every region manager that owns volumes. The central hub
does not need S3 credentials unless it also hosts workspaces. The hub reports
backup availability if any region has backups enabled, and volume backup,
restore, and delete requests are proxied to the region that owns the volume.

All regions can share one bucket and prefix. Object keys include the volume
name, which is unique within the cluster. Use a separate prefix per region if
you want independent lifecycle rules or retention policies.

Copying a backup to a volume in another region requires the source and target
regions to use the same bucket and prefix, because the restore is performed by
the target region. The hub checks access to the source volume and the target
region checks access to the target volume.

## Create, restore, and delete backups

1. Open **Volumes** and select **Backups** on a volume.
2. **Create backup** streams a gzip tar archive of the volume root to S3. If the
   workspace is running, the dialog shows a live-backup warning; the helper is
   scheduled on the same node and mounts the volume read-only. The object key is
   `<prefix>/<volume>/<YYYYMMDDTHHMMSSZ>.tar.gz`.
3. **Restore** opens a dialog with a **Restore into** selector. The target must
   be `Bound`; if it belongs to a workspace, that workspace must be **Stopped**.
   The selector defaults to the source volume but can point at another volume,
   which copies the archive into that volume without changing the backup object.
4. **Delete existing files before restoring** removes everything in the target
   volume first; without it, the archive is extracted over the current contents
   and extra files are kept.
5. **Delete backup** removes only the selected object from S3.

The archive contains the whole volume root, including dotfiles. Restored files
keep the UID/GID recorded in the archive; Poddy's workspace images use UID/GID
`1000`.

## API

The UI uses the same API:

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/volumes` | List volumes, summary, and backup availability. Add `?region=eu` to filter. |
| `DELETE` | `/api/volumes/{name}` | Delete an unassigned volume. |
| `GET` | `/api/volumes/{name}/backups` | List backups for a volume. |
| `POST` | `/api/volumes/{name}/backups` | Create a backup. |
| `DELETE` | `/api/volumes/{name}/backups?key=...` | Delete a backup object. |
| `POST` | `/api/volumes/{name}/restore` | Restore `{"key": "...", "clean": true}`. Add `"target": "other-volume", "targetRegion": "us"` to copy the backup into another volume. |

Volume actions accept `?region=<name>` for the source volume; without it, the
hub locates the source region. For a copy, set `targetRegion` to the target
volume's region.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| `501 volume backups are not configured` | Set the endpoint, bucket, access key, and secret key; check the portal startup log. |
| `409 stop the workspace before restoring` | Restore always requires a stopped target workspace. |
| `400 invalid backup key` | The key does not belong to the source volume prefix. |
| `404 target volume not found` | The target volume is missing or not visible to your account. |
| `409 wait for the workspace to be running or stopped` | The workspace is pending, terminating, or failed; wait for it to settle before backing up. |
| `409 live backup is not supported for ReadWriteOncePod volumes` | Stop the workspace; live backup cannot mount an RWOP volume a second time. |
| Live backup fails to schedule or mount | Check that the workspace node has room for the helper pod and that the CSI driver allows a second read-only mount on the same node. |
| Helper pod stuck in `ErrImagePull` | Set `backup.helperImage` to an image available to the cluster, or allow the node to pull `busybox:1.36`. |
| Archive is empty or very small | The helper cannot read the volume. Check PVC ownership and that the helper pod mounted the expected claim. |
| Restore cannot overwrite files | Enable **Delete existing files before restoring**, or ensure the helper can write the existing file ownership. |
