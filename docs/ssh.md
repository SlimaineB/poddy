# SSH access

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
     │   (outbound WebSocket)                      workspace sshd
     │
     └── Kubernetes port-forward ──────────────────► workspace pod :22
         (only when ssh.requireAgent=false)
```

The workspace pod runs `poddy-agent`. It opens an outbound WebSocket to the
bastion and multiplexes TCP streams back to the workspace's loopback SSH port.
The bastion never accepts an inbound connection from the workspace, and the
agent only permits `127.0.0.1:22`, `localhost:22`, or `[::1]:22`.

Desktop IDEs use the same bastion: VS Code and Cursor through the Poddy
extension, and PyCharm through JetBrains Gateway and the generated SSH config.
Gateway must parse `~/.ssh/config`; the generated block uses `ProxyCommand`
because Gateway does not support `ProxyJump`.

The portal signs a workspace-bound agent token and injects it into the pod as
`PODDY_AGENT_TOKEN`, together with `PODDY_AGENT_URL` and
`PODDY_WORKSPACE_NAME`. The bastion verifies the token with the shared
`PODDY_AGENT_KEY`. Set `ssh.requireAgent=true` to reject connections when the
agent is not connected.

Existing workspace pods must be stopped and started after upgrading so they
receive the agent environment and run the new image. Custom workspace images
must include `/usr/local/bin/poddy-agent` and start it from the container
entrypoint. The repository's `Dockerfile.workspace-base`
builds and installs the agent, and `scripts/poddy-ssh-start` launches it before
`sshd`. If an image cannot include the agent, set `ssh.requireAgent=false` to
keep the Kubernetes port-forward fallback.

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

## Helm configuration

```yaml
ssh:
  requireAgent: true
  certTtl: 15m

  agent:
    enabled: true
    port: 8091
    tokenTtl: 168h
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
`ssh.agent` Secret; cross-region agent keys are not required.

## Security notes

- Treat the CA private key as a cluster credential. It can mint SSH access to
  every workspace that trusts it. The bastion only needs the public key in
  principle, but the chart currently mounts the private key into both portal and
  bastion for simplicity; restrict Secret access accordingly.
- Rotating the CA or changing `ssh.requireAgent` updates the CA line only for
  workspaces created or restarted afterwards. Restart existing workspaces to
  apply the new line.
- Agent tokens are bearer credentials. Anyone who can read a workspace pod's
  environment can impersonate that workspace's agent. Keep `pods/exec` and
  secret access restricted. Tokens default to 7 days and are not refreshed in
  place; restart long-running workspaces or raise `ssh.agent.tokenTtl` before
  the token expires.
- `ssh.requireAgent=true` removes the Kubernetes port-forward fallback. Keep it
  enabled in production and keep the bastion's `pods/portforward` permission
  only for controlled rollback.
- Administrator certificates (`poddy-admin=true`) can reach every workspace.
  Keep their TTL short.
