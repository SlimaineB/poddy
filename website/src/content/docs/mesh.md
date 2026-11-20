---
title: "Headscale mesh"
description: "Replace the SSH bastion with a self-hosted WireGuard tailnet and DERP relays."
---

Poddy can run its SSH data plane on a WireGuard tailnet instead of a central
SSH bastion. Workspace agents join a self-hosted [Headscale](https://headscale.net/)
control server, users join the same tailnet, and SSH goes directly to
`ws-<workspace>` over WireGuard. When a direct path is unavailable, tsnet falls
back to DERP relays.

```text
laptop ──WireGuard/DERP──► poddy-agent (ws-demo) ──► 127.0.0.1:22
                                                workspace sshd
```

The portal is still the control plane: it stores users, keys and access rules,
but it no longer listens on port 2222 and the chart does not create an SSH
Service when `mesh.enabled=true`. The browser terminal continues to use the
Kubernetes/region API and is unaffected.

## Install Headscale through the chart

The chart runs a single-replica Headscale server with SQLite and a PVC. It
also runs the Headscale embedded DERP relay, so no Tailscale DERP service or
external DERP container is required. Tailscale's public DERP map is not loaded
by default.

```yaml
mesh:
  headscale:
    enabled: true
    host: mesh.example.com
    ingressClassName: traefik
    tlsSecret: mesh-example-tls
    annotations:
      cert-manager.io/cluster-issuer: letsencrypt
  derp:
    embedded: true
    stunPort: 30347
    service:
      stunNodePort: 30347
  dex:
    enabled: true
    users:
      - email: admin@example.com
        password: change-me
```

Headscale must be reachable over HTTPS by workspaces and user laptops. Finish
the ingress/TLS setup before enabling the mesh data plane. The embedded DERP
TCP endpoint is served on the same HTTPS host; expose the STUN UDP port through
a LoadBalancer or a NodePort whose number equals `mesh.derp.stunPort`.

With `mesh.dex.enabled=true`, Dex runs in the same chart and is exposed under
`https://<mesh.headscale.host>/dex`. Headscale OIDC is configured automatically,
so there is no external identity-provider dependency. Prefer `passwordHash`
(bcrypt) over `password` for production; Helm can generate one locally:

```bash
htpasswd -bnBC 10 "" 'change-me' | tr -d ':\n'
```

## Create the workspace auth key

```bash
kubectl -n poddy-system exec deploy/poddy-headscale -- \
  headscale users create poddy
kubectl -n poddy-system exec deploy/poddy-headscale -- \
  headscale preauthkeys create --user poddy --reusable --expiration 720h
```

Enable the mesh with that key:

```bash
helm upgrade poddy ./deploy/helm/poddy -n poddy-system \
  --reuse-values \
  --set mesh.enabled=true \
  --set mesh.authKey=tskey-auth-...
```

Existing workspaces must be stopped and started so their agent sidecar receives
the new `PODDY_MESH_*` environment variables.

## Join users

Users install Tailscale and join the same control server once. The browser
opens the in-chart Dex login page:

```bash
tailscale up --login-server=https://mesh.example.com
```

The SSH dialog shows the direct `ws-<workspace>` host and the generated
`~/.ssh/config` block. No `ProxyCommand` is used. Desktop VS Code/Cursor still
enrolls a workspace key through the portal, then connects directly over the
tailnet.

For automated onboarding without the browser flow, create a short-lived user
key:

```bash
kubectl -n poddy-system exec deploy/poddy-headscale -- \
  headscale preauthkeys create --user alice --expiration 24h
tailscale up --login-server=https://mesh.example.com --authkey=tskey-auth-...
```

Add Dex users under `mesh.dex.users`; each entry needs `email` and either
`passwordHash` or `password`. To federate an existing IdP later, use Dex
connectors or set `mesh.headscale.oidc` to an external issuer instead of
`mesh.dex.enabled`.

## ACLs

Restrict user devices to the workspace SSH port. With a tagged workspace key
(`--tags tag:poddy-workspace`), a minimal Headscale policy looks like:

```json
{
  "tagOwners": {
    "tag:poddy-workspace": ["autogroup:admin"]
  },
  "acls": [
    {
      "action": "accept",
      "src": ["autogroup:member"],
      "dst": ["tag:poddy-workspace:22"]
    }
  ]
}
```

Apply it with `headscale policy set -f policy.json` from the Headscale pod (or
through the Headscale API). The agent only forwards mesh port 22 to the local
sshd, so SSH key authorization is still enforced inside the workspace.

## Network policy

The workspace NetworkPolicy automatically allows the WireGuard/DERP traffic
when `networkPolicy.enabled=true`:

- UDP 41641 for direct WireGuard paths.
- TCP 443 for DERP over the Headscale ingress.
- UDP `mesh.derp.stunPort` (3478 by default) for STUN and UDP 41641 for direct
  WireGuard paths. Workspace mesh agents bind UDP 41641 so the workspace
  NetworkPolicy can allow direct peer traffic on a predictable port.
- TCP 8080 to the in-cluster Headscale pod.

If you use an external control server or a custom DERP server on non-standard
ports, add the endpoints to `networkPolicy.workspace.egress.allowedCIDRs`.

## Air-gapped operation

The mesh path makes no runtime calls to Tailscale, an external IdP, or public
DERP servers. Prepare the following before installing:

- Mirror the Poddy, Headscale, Dex, workspace template, PostgreSQL, and demo
  images into the internal registry and set `image.repository`,
  `imagePullSecrets`, and `workspace.imageRegistry`/`imageRegistryMap`.
- Use an internal DNS name and TLS Secret for `mesh.headscale.host`; set
  `mesh.headscale.tlsSecret` or `mesh.headscale.ingressClassName`.
- Leave `mesh.derpUrls` empty. The chart starts the embedded DERP server and
  sets `disable_check_updates: true`.
- Keep `mesh.headscale.nameservers` empty, or set it to DNS servers reachable
  by user devices. Poddy does not inject `1.1.1.1` or any public resolver.
- Open TCP 443 to the ingress and UDP `mesh.derp.stunPort` to the Headscale
  Service. Direct WireGuard uses UDP 41641.

For a fully offline demo, use HTTP with `mesh.headscale.serverUrl` set to the
internal URL and disable the ingress TLS requirement. This works for in-cluster
direct paths. Tailscale clients outside the cluster need the HTTPS ingress:
the embedded DERP server only serves relay traffic over TLS, so an HTTP-only
Headscale cannot relay a laptop connection to a workspace.

## Multi-region demo

`deploy/demo/demo.sh` installs the single-region demo. To add a hub with mesh
SSH plus two regions on one k3s cluster:

```bash
DEMO_MESH=1 DEMO_REGIONS="eu us" deploy/demo/demo.sh
```

The script installs Headscale and Dex in `poddy-system`, creates the workspace
preauth key, enables mesh SSH on the hub, then installs `poddy-eu` and
`poddy-us` with the same control URL and key. Workspaces in every region get
the mesh sidecar and appear as `ws-<workspace>` on the tailnet.
