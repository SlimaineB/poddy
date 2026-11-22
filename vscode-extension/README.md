# Poddy Remote

VS Code and Cursor extension for Poddy. It handles the
`vscode://slimaineb.poddy-remote/...` or `cursor://slimaineb.poddy-remote/...`
link opened by the Poddy **VS Code** or **Cursor** button: it creates one
dedicated machine key, enrolls the public key, writes a single
`Host poddy-desktop-*` block to the real `~/.ssh/config`, waits for the
workspace to accept SSH, and opens Remote-SSH on `/home/dev`. Legacy
per-workspace blocks and `poddy_*_ed25519` keys are removed automatically.

The VSIX bundles a small `poddy-connect` helper for Linux, macOS, and Windows.
The SSH config uses it as `ProxyCommand`. The signed desktop ticket selects one
of two transports:

- **Mesh (preferred when enabled).** `poddy-connect --mode mesh` joins the
  Headscale/Tailscale tailnet with a preauth key and dials `ws-<workspace>` over
  WireGuard. Users do not install Tailscale or run `tailscale up`. The extension
  stores one `mesh/authkey`, one `mesh/ca.crt`, and one shared `mesh/state`
  directory; each ProxyCommand invocation gets its own throwaway tsnet state
  and waits up to four minutes for a cold workspace to accept SSH.
- **Portal WebSocket (fallback).** `poddy-connect --portal` opens an
  authenticated WebSocket to `/api/desktop/ssh`; the portal reaches the
  workspace through the Kubernetes port-forward API or the regional tunnel.
  The ticket is stored as `portal/<workspace>.ticket`; this path does not need
  mesh, Tailscale, or the portal SSH listener.

The portal advertises the transport in the desktop payload: mesh when
`mesh.enabled=true` and a client auth key is configured, otherwise portal when
the portal can dial workspaces. Older portals that return no transport keep the
SSH jump-host fallback. There is no user-facing transport setting; the extension
follows the portal.

The extension runs on the local UI side (`extensionKind: ui`). It is not
installed in the workspace and requires the local `ssh` and `ssh-keygen` binaries
(OpenSSH on Windows).

## Install

Download the latest `poddy-remote-<version>.vsix` from the
[GitHub releases](https://github.com/SlimaineB/poddy/releases) and install it:

```bash
code --install-extension poddy-remote-<version>.vsix
# or
cursor --install-extension poddy-remote-<version>.vsix
```

Or use **Extensions** in VS Code or Cursor, open the `...` menu, and choose
**Install from VSIX...**.

JetBrains IDEs do not use this extension. Use the **PyCharm** button and
JetBrains Gateway with the SSH config from the **SSH** dialog.

## Build locally

```bash
make vscode-helper
cd vscode-extension
npm ci
npm run package
```

`make vscode-helper` cross-compiles `cmd/poddy-connect` into
`vscode-extension/bin/`; the VSIX is written next to `package.json` and is
ignored by Git. The
`VS Code extension` GitHub Actions workflow builds it on changes and attaches
the VSIX to a `v*` release when the tag matches the extension version. Other
app tags skip extension packaging and publishing instead of failing.

## Release

1. Bump `version` in `vscode-extension/package.json`.
2. Update `internal/extension/extension.json` with the same version, tag, and
   asset URLs. The Go test `internal/extension` fails when the two versions
   differ.
3. Commit and push the change.
4. Run the **VS Code extension** workflow from the Actions tab with the new
   version, or push a matching `poddy-remote-v<version>` tag. The extension
   tag keeps the VSIX release independent from application tags; a plain
   `v<version>` tag also works when the app and extension versions match. The
   workflow validates the tag, builds the VSIX, and creates or updates the
   GitHub release.

The portal reads the embedded metadata and shows the extension version and a
download link in the top bar and in the **Connect via SSH** dialog. Set
`vscodeExtension.version`, `vscodeExtension.releaseUrl`, or
`vscodeExtension.downloadUrl` in the Helm values to override the embedded
metadata.
