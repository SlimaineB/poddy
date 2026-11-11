# Poddy Remote

VS Code and Cursor extension for Poddy. It handles the
`vscode://slimaineb.poddy-remote/...` or `cursor://slimaineb.poddy-remote/...`
link opened by the Poddy **VS Code** or **Cursor** button: it creates a dedicated
machine key, enrolls the public key, writes a `# >>> poddy:<workspace>` block to
the real `~/.ssh/config`, waits for the workspace to accept SSH, and opens
Remote-SSH on `/home/dev`.

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
cd vscode-extension
npm ci
npm run package
```

The VSIX is written next to `package.json` and is ignored by Git. The
`VS Code extension` GitHub Actions workflow builds it on changes and attaches
the VSIX to `v*` releases.

## Release

1. Bump `version` in `vscode-extension/package.json`.
2. Update `internal/extension/extension.json` with the same version, tag, and
   asset URLs. The Go test `internal/extension` fails when the two versions
   differ.
3. Commit and push the change.
4. Run the **VS Code extension** workflow from the Actions tab with the new
   version, or push a matching `v<version>` tag. The workflow validates the tag,
   creates it when needed, builds the VSIX, and creates or updates the GitHub
   release.

The portal reads the embedded metadata and shows the extension version and a
download link in the top bar and in the **Connect via SSH** dialog. Set
`vscodeExtension.version`, `vscodeExtension.releaseUrl`, or
`vscodeExtension.downloadUrl` in the Helm values to override the embedded
metadata.
