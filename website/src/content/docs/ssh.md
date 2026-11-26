---
title: "SSH access"
description: "Connect to workspaces through the Headscale mesh, an authenticated portal WebSocket, or the portal SSH listener."
---

Poddy has two transports for the desktop Poddy extension, plus the portal SSH
listener for other clients:

- **Mesh** when `mesh.enabled=true`: workspaces join a WireGuard tailnet and the
  extension dials `ws-<workspace>` over WireGuard/DERP; see
  [Headscale mesh](/mesh). The portal does not need an SSH listener.
- **Portal WebSocket** otherwise: the extension opens an authenticated WebSocket
  to `/api/desktop/ssh`, and the portal dials the workspace through the
  Kubernetes port-forward API or the regional tunnel.

When mesh is disabled, the portal SSH listener (`sshHost:sshPort`) serves the
generated `Host poddy-ssh-*` config, manual `ssh`, and JetBrains Gateway.
`poddy ssh` uses the mesh when it is enabled.

## Connection paths

```text
Desktop VS Code / Cursor (Poddy extension → poddy-connect)
  ├─ mesh: WireGuard/DERP ─────────────────────► ws-<workspace>:22
  │                                              workspace sshd
  └─ portal: WSS /api/desktop/ssh
       └─ portal ── port-forward / region tunnel ─► workspace pod :22

ssh / JetBrains Gateway (when mesh is disabled)
  └─ portal :2222
       ├─ workspace agent tunnel ─► poddy-agent ─► 127.0.0.1:22
       │  (outbound WebSocket)      (workspace)      workspace sshd
       └─ Kubernetes port-forward ──────────────────► workspace pod :22
          (only when ssh.requireAgent=false)
```

### Desktop transport selection

The portal returns the transport in the signed desktop payload:

1. `mesh` if `mesh.enabled=true` and a client auth key is configured
   (`PODDY_MESH_CLIENT_AUTH_KEY`, which defaults to `mesh.authKey`).
2. `portal` otherwise, when the portal can dial workspaces.
3. Older portals that return no transport keep the SSH jump-host fallback.

The extension follows this value automatically; users do not choose a mode. In
mesh mode, `poddy-connect` joins Headscale with the client preauth key and dials
the `ws-<workspace>` peer. In portal mode, it sends a short-lived desktop ticket
to `/api/desktop/ssh`, and the portal opens the TCP stream. The portal mode works
without a public SSH Service and is available when mesh is disabled or not
configured.

The workspace runs `poddy-agent` inside the main workspace container by default.
It opens an outbound WebSocket to the portal and multiplexes TCP streams back to
the workspace's loopback SSH port. The portal never accepts an inbound connection
from the workspace, and the agent only permits `127.0.0.1:22`, `localhost:22`,
or `[::1]:22`. The agent shares the workspace cgroup; set
`ssh.agent.sidecar=true` to run it in a dedicated sidecar with its own memory
limit and keep the tunnel alive when the workspace OOMs.

Desktop IDEs use the transport above: VS Code and Cursor through the Poddy
extension, and PyCharm through JetBrains Gateway and the generated SSH config.
Gateway must parse `~/.ssh/config`; the generated block uses `ProxyCommand`
because Gateway does not support `ProxyJump`.

Desktop VS Code installs its server in `~/.vscode-server` inside the workspace
home. Poddy does not manage that directory. With a persistent home volume the
server and remote extensions survive stop/start and restarts. With
`storage: ""` (ephemeral home) the pod uses an `emptyDir`, so VS Code downloads
and installs the server again after each restart. Choose a persistent home size
in the create dialog when you use desktop VS Code. The browser IDE sidecar
stores its state in `/home/dev/.openvscode-server` and runs as UID 1000, so it
follows the same persistence rule.

The portal signs a workspace-bound agent token and stores it in a per-workspace
Secret. The agent receives it as `PODDY_AGENT_TOKEN` through a
`SecretKeyRef`, not as a plaintext value in the pod spec, together with
`PODDY_AGENT_URL`, `PODDY_WORKSPACE_NAME`, `PODDY_AGENT_TOKEN_FILE`, and
`PODDY_AGENT_REFRESH_FILE`. The Secret is also mounted read-only at
`/var/run/poddy-agent/token`. The portal rewrites the Secret at half the token
lifetime, kubelet projects the update into the running pod, and the agent reads
the mounted file before every reconnect. Tokens default to one hour
(`ssh.agent.tokenTtl`) and are also renewed automatically over the established
tunnel, so a disconnect longer than one hour can recover without recreating the
workspace. Set `ssh.requireAgent=true` to reject connections when the agent is
not connected.

The token is signed with a key derived from the shared `PODDY_AGENT_KEY` and
the workspace name, so a token for one workspace cannot be replayed against
another. The portal verifies the signature before accepting the tunnel.

Existing workspace pods must be stopped and started after upgrading so they
receive the new agent configuration. Custom workspace images must include
`/usr/local/bin/poddy-agent` and start it from the container entrypoint: the
repository's `docker/workspace/base/Dockerfile` builds and installs the agent,
and `scripts/poddy-ssh-start` launches it before `sshd`. The bundled workspace
images already include it.

Set `ssh.agent.sidecar=true` to run the agent in a dedicated sidecar instead.
The sidecar uses the portal image, so custom workspace images do not need the
agent; set `ssh.agent.image` to use a different image, which must contain
`/usr/local/bin/poddy-agent`. If an image cannot include the agent and you do
not use the sidecar, set `ssh.requireAgent=false` to keep the Kubernetes
port-forward fallback.

## Memory pressure

`sshd` and, by default, `poddy-agent` share the workspace container's cgroup, so
a memory spike can OOM-kill either process and disconnect active sessions. Poddy
does not grant the workspace extra capabilities or change OOM scores to prevent
this. The agent reconnects when the workspace recovers; SSH sessions themselves
are not resumable and must be reconnected. Set `ssh.agent.sidecar=true` to give
the agent its own memory limit so the tunnel survives a workspace OOM kill.

Use workspace CPU/memory requests and limits, and avoid unbounded builds or
processes in the workspace. The workspace image must still run
`scripts/poddy-ssh-start` (or an equivalent entrypoint) to prepare
`authorized_keys` and start `sshd`.

## Portal failures and reconnects

The workspace and its agent stay running when a portal pod fails. Active SSH
sessions through that pod are terminated because the TCP connection is anchored
to that process; SSH has no session resumption. The agent reconnects to another
portal replica, and new sessions use the replica that owns the agent tunnel.

Poddy limits the outage in three ways:

- The agent retries quickly with exponential backoff capped at 5 seconds and
  adds jitter so many workspaces do not reconnect at the same instant.
- Agent tunnels use WebSocket ping/pong heartbeats. A dead or half-open tunnel
  is detected within 20 seconds instead of waiting for a TCP timeout.
- On `SIGTERM`, the portal rejects new SSH connections and agent tunnels
  immediately, keeps existing SSH sessions until `ssh.drainTimeout`, and closes
  the listener after `ssh.drainDelay` so Kubernetes can remove the endpoint
  first.

For production, run at least two portal replicas with a shared
`ssh.existingHostKeySecret` (or let the chart generate one). The portal PDB is
created automatically above one replica, and
`portal.terminationGracePeriodSeconds` must stay above
`drainDelay + drainTimeout`. The rolling update strategy already uses
`maxUnavailable: 0`, so a healthy replacement starts before an old pod is
terminated. A hard crash still drops sessions that were attached to that pod;
replicas reduce the blast radius but cannot migrate an open SSH connection.

## Short-lived certificates

The portal can issue a short-lived OpenSSH user certificate instead of relying
on long-lived public keys in every workspace:

- The portal signs with the CA private key from `PODDY_SSH_CA_KEY_FILE`.
- The portal verifies the certificate with the same CA.
- Workspace `authorized_keys` contains a CA line:

  ```text
  cert-authority,principals="dev",from="127.0.0.1,::1" ssh-ed25519 AAAA... poddy-ca
  ```

  With `ssh.requireAgent=false`, the `from=` restriction is omitted so
  certificates also work through the Kubernetes port-forward fallback, whose
  connections do not originate from workspace loopback.

- The certificate principal is `dev`, matching the workspace SSH user.
- The owner identity is stored in the certificate `KeyId`.
- The certificate carries `poddy-workspace` when it is scoped to one workspace
  and `poddy-admin=true` for administrators.

The portal checks the certificate owner and optional workspace scope before
opening a `direct-tcpip` channel. Workspace sshd performs the final
authentication against the CA line. Legacy user keys remain valid unless you
remove them from the key registry.

### Multi-region SSH

With `regions` (or `regionsExistingSecret`) configured, the hub portal reaches
region workspaces through the **outbound** region tunnel: it opens
`tcp:<workspace>:22`, and the region manager port-forwards to the pod. No
regional SSH Service, load balancer, peering, or inbound firewall rule is
required, and `sshHost`/`sshPort` stay pointed at the hub for every workspace.
A portal replica that does not own the region tunnel looks up the owner in a
Kubernetes Lease and forwards the raw stream to that replica.

Requirements and limits:

- Hub and regions must mount the **same SSH CA** (for example the same
  `ssh.ca.existingSecret`), because the hub signs and the region portal verifies.
- Relayed sessions accept a short-lived certificate or a self-service public key
  authorized for the workspace owner. Region-prefixed aliases such as
  `poddy-ssh-eu-<workspace>` resolve to the same workspace as `poddy-ssh-<workspace>`.
- Only port 22 is forwarded, and administrators still need owner approval.

### Issue a certificate

The endpoint is `POST /api/me/ssh-certificate`. It accepts a JSON body with the
user's public key and an optional workspace:

```json
{
  "key": "ssh-ed25519 AAAA... user@example.com",
  "workspace": "alice-dev"
}
```

The response contains the certificate line:

```json
{
  "certificate": "ssh-ed25519-cert-v01@openssh.com AAAA...",
  "principal": "dev",
  "owner": "alice@example.com",
  "workspace": "alice-dev",
  "expiresAt": "2026-01-01T12:15:00Z",
  "fingerprint": "SHA256:..."
}
```

Save the certificate next to the private key so OpenSSH loads it
automatically:

```bash
ssh-keygen -t ed25519 -f ~/.ssh/poddy -N ''

curl -fsS -X POST https://poddy.example.com/api/me/ssh-certificate \
  -H 'Content-Type: application/json' \
  -b cookies.txt \
  -d "{\"key\":\"$(cat ~/.ssh/poddy.pub)\",\"workspace\":\"alice-dev\"}" \
  | jq -r .certificate > ~/.ssh/poddy-cert.pub

chmod 600 ~/.ssh/poddy ~/.ssh/poddy-cert.pub

ssh -i ~/.ssh/poddy \
  -o CertificateFile=~/.ssh/poddy-cert.pub \
  -J dev@ssh.example.com \
  dev@alice-dev
```

The same certificate authenticates the outer connection to the portal and the
inner connection to the workspace. Certificates default to 15 minutes
(`ssh.certTtl` / `PODDY_SSH_CERT_TTL`).

If `workspace` is omitted, the certificate can access any workspace owned by
the certificate owner. If it is present, the portal restricts the certificate
to that workspace.

## Poddy CLI

The `poddy` binary includes a user CLI for laptops. It stores its session in
`~/.config/poddy/config.json` (mode `0600`) and can use a browser login or an
API token.

### Install

Download `poddy_<version>_<os>_<arch>` from the
[latest GitHub release](https://github.com/SlimaineB/poddy/releases/latest).
macOS and Linux builds are available for `amd64` and `arm64`; Windows builds
end in `.exe`. Verify the download with the release `checksums.txt`, then put
the binary on your `PATH`:

```bash
chmod +x poddy_v0.1.10_darwin_arm64
sudo mv poddy_v0.1.10_darwin_arm64 /usr/local/bin/poddy
poddy version
```

From a source checkout, build it with `go build -o poddy ./cmd/poddy`. The
workspace image also includes `poddy`, but laptop users should install the
release binary.

```bash
poddy login --portal https://poddy.example.com
poddy list
poddy config --install
poddy ssh alice-dev
poddy cp alice-dev:/home/dev/dataset training-gpu:/home/dev/
poddy port-forward alice-dev 8080:80
```

`poddy login` starts a loopback callback and opens the portal. The portal
returns a short-lived code that the CLI exchanges for a session token, protected
by PKCE (`S256`) so an intercepted code is not usable without the verifier.
Login codes are single-use, and revoked tokens are rejected immediately. The
CLI login endpoints are rate-limited per client IP. Use `--token` or
`PODDY_TOKEN` for automation. `poddy config` prints the same
`ProxyCommand` configuration as the portal; `--install` merges a managed block
into `~/.ssh/config` without touching the rest of the file. `poddy ssh` creates
a per-workspace key, requests a short-lived certificate when certificates are
enabled, falls back to enrolling the public key, then runs the system `ssh`
client. When the mesh is enabled, `poddy ssh`, `poddy cp`, and
`poddy port-forward` use the bundled helper to join the Headscale/Tailscale
tailnet and dial the workspace over WireGuard instead of the portal SSH
endpoint. `poddy port-forward` only forwards the requested local port.
`poddy cp` streams a `tar` archive between two workspaces through the same SSH
path; it works across regions, does not store the data in the portal, and
applies the same workspace access checks. Copying a directory is
recursive, and an existing destination directory receives the source basename,
matching `cp` semantics. Both workspaces must be running and have `tar`
installed. Use `--key` to reuse one local private key; the CLI creates separate
temporary copies so each workspace gets its own short-lived certificate.
`poddy logout` revokes the token server-side and removes the local config.

## Helm configuration

```yaml
# SSH runs inside the portal Deployment, so replicaCount also scales SSH.
replicaCount: 2

ssh:
  # Reject new connections immediately, then wait for endpoint removal.
  drainDelay: 3s
  # Existing SSH sessions survive graceful pod shutdown for this long.
  drainTimeout: 20s
  terminationGracePeriodSeconds: 30
  requireAgent: true
  certTtl: 15m

  agent:
    enabled: true
    # Agent runs in the workspace container by default. Set true for an
    # isolated sidecar with its own memory limit.
    sidecar: false
    # Only used when sidecar=true. Defaults to the portal image, which contains
    # /usr/local/bin/poddy-agent.
    image: ""
    port: 8091
    tokenTtl: 1h
    # url: ""  # defaults to http://<fullname>-agent.<namespace>.svc:<port>

  ca:
    # Leave key empty to generate and preserve a random CA key.
    # key: |
    #   -----BEGIN PRIVATE KEY-----
    #   ...
    existingSecret: ""
    keyKey: ssh-ca-key
```

The chart creates:

- `<fullname>-ssh`, a Service for the SSH listener on the portal.
- `<fullname>-agent`, a ClusterIP Service for the workspace agent listener.
- `<fullname>-agent`, a Secret containing the shared agent HMAC key.
- `<fullname>-ssh-host-key`, a Secret containing the SSH host key.
- `<fullname>-ssh-ca`, a Secret containing the SSH CA private key.

The portal mounts the CA Secret and uses it to sign and verify certificates. Back
up the CA Secret if you need certificates issued before a restore to remain valid.

## Multi-region

The hub portal is the only SSH entry point. Region managers run with
`regionManager.enabled=true`, do not listen for SSH, and never require an agent
sidecar. The hub reaches a region workspace through the region manager's
outbound tunnel: it opens a `tcp:<workspace>:22` stream, the region manager
port-forwards to the pod, and the SSH session is spliced end to end.

Mount the same CA Secret into the hub and every region, for example by setting
`ssh.ca.existingSecret` to a Secret replicated to each release namespace.
Otherwise a certificate issued by the hub will not verify at a region manager.

Agent tokens are scoped to one workspace and are signed by the hub that creates
the workspace. Region workspaces do not run the agent tunnel; they rely on the
region tunnel and Kubernetes port-forward inside the region. Tokens are
short-lived, are renewed while the agent stays connected, and are refreshed in
the workspace Secret so a reconnecting agent picks up a valid token.

`poddy config` and `poddy ssh` always target the hub SSH endpoint. The hub
routes each workspace to the replica that owns its agent or region tunnel, so
multi-region workspaces use the same `~/.ssh/config` block.

## Security notes

- Treat the CA private key as a cluster credential. It can mint SSH access to
  every workspace that trusts it. The chart mounts the private key into the
  portal; restrict Secret access accordingly.
- Rotating the CA or changing `ssh.requireAgent` updates the CA line only for
  workspaces created or restarted afterwards. Restart existing workspaces to
  apply the new line.
- Agent tokens are bearer credentials scoped to one workspace. Anyone who can
  read a running workspace pod or its agent token Secret can impersonate that
  workspace's agent. Keep `pods/exec` and Secret access restricted. Tokens
  default to one hour, are renewed over the tunnel, are refreshed in the
  workspace Secret, and are workspace-scoped; a token cannot be retargeted at
  another workspace.
- Workspace host keys are stored in the persistent home at
  `/home/dev/.ssh/poddy_ssh_host_ed25519_key`, and generated SSH configs use
  `StrictHostKeyChecking accept-new` instead of disabling verification. The
  first connection pins the key; a later change is rejected. Custom templates
  without a persistent `/home/dev` lose that pin across pod recreation.
- The workspace container already runs as root for `sshd`, and `dev` has
  passwordless sudo. If you use `ssh.agent.sidecar=true` with the portal image,
  add its pull Secret to `workspace.imagePullSecrets` so workspace nodes can
  start the sidecar.
- `ssh.requireAgent=true` removes the Kubernetes port-forward fallback. Keep it
  enabled in production. The Helm chart only grants the portal
  `pods/portforward` when `ssh.requireAgent=false`.
- Administrator certificates (`poddy-admin=true`) can reach every workspace.
  Keep their TTL short. Administrator certificates are also subject to the
  workspace owner's approval; see
  [Workspace presence and owner approval](workspace-access.md).
