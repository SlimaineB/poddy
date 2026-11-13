# Workspace presence and owner approval

Global admins can see and manage every workspace, but **entering** someone
else's workspace is gated. The owner must approve an admin before the admin can
open a terminal, an IDE, a port, or an SSH session. The portal also shows who is
currently connected to each workspace.

This page covers the approval flow, what is gated, and where the state lives.

## What is gated

The approval check applies to interactive access only:

- the web terminal;
- the desktop and browser IDE;
- IDE handoff links (VS Code, Cursor);
- port forwarding, including shared port links;
- SSH sessions opened with an **administrator certificate**.

These capabilities stay available to admins without approval:

- workspace details, manifests, and logs;
- stop, start, and delete;
- links and volumes;
- RBAC and template management.

Owners always enter their own workspace. In local trusted mode
(`-auth-method=none`) the approval check is bypassed. Workspaces created before
the owner annotation existed have no owner and remain open to admins.

## Request, approve, revoke

1. An admin opens a workspace they do not own. The action buttons explain that
   owner approval is required.
2. The admin clicks **Request access**. The owner sees the request in the
   workspace **Access** panel.
3. The owner clicks **Approve** or **Deny**. Approval creates a grant for that
   admin only.
4. The admin can enter the workspace. The grant expires after **30 minutes**;
   the admin can request access again afterwards.
5. The owner can **Revoke** a grant at any time.

The owner and admins see pending requests and active grants in the Access panel.
The same panel lists current presence, with one entry per actor and the kinds of
session they have open.

## Presence

Presence combines two sources:

- **Portal sessions**: terminal, desktop/browser IDE, and port-forward sessions.
  The portal refreshes presence while the session is active.
- **SSH bastion sessions**: a certificate-authenticated SSH session registers
  presence when the channel opens and clears it when it closes.

Presence uses a 30-second heartbeat and a 2-minute TTL. A session that closes
normally is removed immediately; a crashed client disappears after the TTL.

Presence is shown on workspace cards, in the workspace **Access** panel, and on
the topology graph (a person badge on the user and workspace nodes).

Presence is advisory. It is not an authorization decision and should not be
used as an audit record.

## State storage

Requests, grants, and presence are stored in PostgreSQL (tables
`poddy.access_requests`, `poddy.access_grants`, and `poddy.access_presence`)
when `PODDY_DATABASE_URL` is configured. The portal and the bastion share the
same database and namespace key, so an approval made in the browser also
unlocks SSH.

This state is mutable and written frequently (presence heartbeats), so it is not
stored in a Kubernetes Secret. Without a database, the portal falls back to an
in-memory store, which only works for a single replica and cannot be shared with
a separate bastion process. Set `PODDY_DATABASE_URL` in production.

Expired grants and stale presence rows are filtered on read and pruned
opportunistically. No Secret permissions are required for access state.

## API

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/api/workspaces/{name}/access` | Read presence, pending requests, and grants |
| `POST` | `/api/workspaces/{name}/access/request` | Request access as the signed-in admin |
| `POST` | `/api/workspaces/{name}/access/approve` | Owner approves `{"actor": "..."}` |
| `POST` | `/api/workspaces/{name}/access/deny` | Owner denies `{"actor": "..."}` |
| `POST` | `/api/workspaces/{name}/access/revoke` | Owner revokes `{"actor": "..."}` |

A blocked interactive request returns HTTP `403` with
`X-Poddy-Access: required` and `{"code":"access_required", ...}`. A user who
cannot see the workspace still receives `404`.

The actions are recorded in the audit trail as `workspace.access.request`,
`workspace.access.approve`, `workspace.access.deny`, and
`workspace.access.revoke`.

## Multi-region

Access state belongs to the region that hosts the workspace. The central portal
proxies access requests to that region, and the region bastion reads the same
PostgreSQL database. Every region must point at the same database; the
`namespace` column isolates tenants. Region listings include the access summary
so the hub can show presence and pending requests without extra calls.

## SSH notes

SSH sessions are gated only when they use an administrator certificate
(`poddy-admin=true`). Administrator keys listed directly in
`ssh.authorizedKeys` with the `admin` or `*` comment are a break-glass path and
are not gated. Keep those keys few and rotate them.

See [SSH access](ssh.md) for certificate setup and [RBAC and ownership](rbac.md)
for the admin model.
