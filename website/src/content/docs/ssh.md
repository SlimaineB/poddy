---
title: "SSH access"
description: "Connect to workspaces over SSH through the outbound agent or the Kubernetes port-forward fallback."
---

poddy supports two SSH connection paths. The outbound workspace agent is the
default in the Helm chart; the Kubernetes port-forward path remains as a
compatibility fallback.

## Connection paths

```text
ssh / VS Code / Cursor / PyCharm
     │
     ▼
poddy-bastion :2222
     │  direct-tcpip (port 22 only)
     │
     ├── workspace agent tunnel ──► poddy-agent ──► 127.0.0.1:22
     │   (outbound WebSocket)      sidecar          workspace sshd
     │
     └── Kubernetes port-forward ──────────────────► workspace pod :22
         (only when ssh.requireAgent=false)
```

The workspace pod runs `poddy-agent` in a dedicated sidecar by default. It opens
an outbound WebSocket to the bastion and multiplexes TCP streams back to the
workspace's loopback SSH port. The bastion never accepts an inbound connection
from the workspace, and the agent only permits `127.0.0.1:22`, `localhost:22`,
or `[::1]:22`. The sidecar has its own memory limit, so a workspace memory spike
cannot OOM-kill the agent and drop every SSH session.

Desktop IDEs use the same bastion: VS Code and Cursor through the Poddy
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

The portal signs a workspace-bound agent token and injects it into the agent
sidecar as `PODDY_AGENT_TOKEN`, together with `PODDY_AGENT_URL`,
`PODDY_WORKSPACE_NAME`, and `PODDY_AGENT_TOKEN_FILE`. The token is signed with a
key derived from the shared `PODDY_AGENT_KEY` and the workspace name, so a token
for one workspace cannot be replayed against another. The bastion verifies the
signature before accepting the tunnel. Tokens default to one hour
(`ssh.agent.tokenTtl`), are renewed automatically over the established tunnel,
and the latest token is persisted in the agent's emptyDir so a container restart
within the same pod does not need a new workspace token. Set
`ssh.requireAgent=true` to reject connections when the agent is not connected.

Existing workspace pods must be stopped and started after upgrading so they
receive the agent sidecar. Custom workspace images do not need to include
`poddy-agent` while `ssh.agent.sidecar=true` (the default): the sidecar uses
the portal image, which contains `/usr/local/bin/poddy-agent`. Set
`ssh.agent.image` to use a different image; it must contain
`/usr/local/bin/poddy-agent`.

With `ssh.agent.sidecar=false`, the agent runs inside the workspace container
instead. In that mode, custom workspace images must include
`/usr/local/bin/poddy-agent` and start it from the container entrypoint. The
repository's `docker/workspace/base/Dockerfile` builds and installs the agent, and
`scripts/poddy-ssh-start` launches it before `sshd`. If an image cannot include
the agent, set `ssh.requireAgent=false` to keep the Kubernetes port-forward
fallback.

## Memory pressure and OOM protection

A workspace memory spike used to share a cgroup with `sshd` and `poddy-agent`,
so the OOM killer could terminate either process and disconnect active
sessions. Poddy now:

- Runs `poddy-agent` in its own container with a 16Mi request and 128Mi limit.
- Grants the workspace container `SYS_RESOURCE` so `poddy-ssh-start` can set
  `sshd`'s `oom_score_adj` to `-1000`.
- Sets the `dev` user's shell to `/usr/local/bin/poddy-shell`, which resets the
  OOM score to `0` before starting the real shell. User processes therefore
  remain killable, while `sshd` and the agent are much less likely to be
  killed.

The workspace image must run `scripts/poddy-ssh-start` as its entrypoint for
the OOM protection to take effect. A template that overrides the workspace
command keeps the agent sidecar but must set the OOM score itself.

## Short-lived certificates

The portal can issue a short-lived OpenSSH user certificate instead of relying
on long-lived public keys in every workspace:

- The portal signs with the CA private key from `PODDY_SSH_CA_KEY_FILE`.
- The bastion verifies the certificate with the same CA.
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

The bastion checks the certificate owner and optional workspace scope before
opening a `direct-tcpip` channel. Workspace sshd performs the final
authentication against the CA line. Legacy user keys remain valid unless you
remove them from the key registry.

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

The same certificate authenticates the outer connection to the bastion and the
inner connection to the workspace. Certificates default to 15 minutes
(`ssh.certTtl` / `PODDY_SSH_CERT_TTL`).

If `workspace` is omitted, the certificate can access any workspace owned by
the certificate owner. If it is present, the bastion restricts the certificate
to that workspace.

## Poddy CLI

The `poddy` binary includes a user CLI for laptops. It stores its session in
`~/.config/poddy/config.json` (mode `0600`) and can use a browser login or an
API token.

```bash
poddy login --portal https://poddy.example.com
poddy list
poddy config --install
poddy ssh alice-dev
poddy port-forward alice-dev 8080:80
```

`poddy login` starts a loopback callback and opens the portal. The portal
returns a short-lived code that the CLI exchanges for a session token. Use
`--token` or `PODDY_TOKEN` for automation. `poddy config` prints the same
`ProxyCommand` configuration as the portal; `--install` merges a managed block
into `~/.ssh/config` without touching the rest of the file. `poddy ssh` creates
a per-workspace key, requests a short-lived certificate when certificates are
enabled, falls back to enrolling the public key, then runs the system `ssh`
client. `poddy port-forward` uses the same tunnel and only forwards the requested
local port.

## Helm configuration

```yaml
ssh:
  requireAgent: true
  certTtl: 15m

  agent:
    enabled: true
    # Dedicated agent sidecar. Disable to keep the legacy in-workspace process.
    sidecar: true
    # Defaults to the portal image, which contains /usr/local/bin/poddy-agent.
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

- `<fullname>-agent`, a ClusterIP Service for the bastion agent listener.
- `<fullname>-agent`, a Secret containing the shared agent HMAC key.
- `<fullname>-ssh-ca`, a Secret containing the SSH CA private key.

Both the portal and the bastion mount the CA Secret. The portal uses it to sign
certificates; the bastion uses it to verify them. Back up the CA Secret if you
need certificates issued before a restore to remain valid.

## Multi-region

The central portal issues certificates, while each region runs its own bastion.
Mount the same CA Secret into the central portal and every region, for example
by setting `ssh.ca.existingSecret` to a Secret replicated to each release
namespace. Otherwise a certificate issued by the hub will not verify at a
region bastion.

Agent tokens are scoped to one workspace and are signed by the release that
creates the workspace. Each region's manager and bastion share their own
`ssh.agent` Secret; cross-region agent keys are not required. Tokens are
short-lived and renew automatically while the agent stays connected.

`poddy config` and `poddy ssh` read the region SSH endpoint from `/api/config`
and the workspace list, so multi-region workspaces use the correct bastion.

## Security notes

- Treat the CA private key as a cluster credential. It can mint SSH access to
  every workspace that trusts it. The bastion only needs the public key in
  principle, but the chart currently mounts the private key into both portal and
  bastion for simplicity; restrict Secret access accordingly.
- Rotating the CA or changing `ssh.requireAgent` updates the CA line only for
  workspaces created or restarted afterwards. Restart existing workspaces to
  apply the new line.
- Agent tokens are bearer credentials scoped to one workspace. Anyone who can
  read a running workspace pod or its StatefulSet template's environment can
  impersonate that workspace's agent. Keep `pods/exec` and Secret access
  restricted. Tokens default to one hour, are renewed over the tunnel, and are
  workspace-scoped; a token cannot be retargeted at another workspace.
- The workspace container is granted `SYS_RESOURCE` so `poddy-ssh-start` can
  lower `sshd`'s OOM score. The container already runs as root for `sshd`, and
  `dev` has passwordless sudo. If the portal image is private, add its pull
  Secret to `workspace.imagePullSecrets` so workspace nodes can start the
  agent sidecar.
- `ssh.requireAgent=true` removes the Kubernetes port-forward fallback. Keep it
  enabled in production. The Helm chart only grants the bastion
  `pods/portforward` when `ssh.requireAgent=false`.
- Administrator certificates (`poddy-admin=true`) can reach every workspace.
  Keep their TTL short. Administrator certificates are also subject to the
  workspace owner's approval; see
  [Workspace presence and owner approval](workspace-access.md).
