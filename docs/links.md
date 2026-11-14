# Template links

Links are buttons shown on workspace cards. They are declared in the workspace
template, so each template controls its own shortcuts and they are versioned
with it. Use them for internal dashboards, documentation, or any protocol the
browser can open. VS Code, Cursor, PyCharm, JupyterLab, and VS Code Web have
built-in buttons; template links are for extra shortcuts.

## Add links to a template

Add a `links` list to `template.yaml`:

```yaml
name: go
displayName: Go
image: ghcr.io/slimaineb/poddy-workspace:latest
links:
  - name: Cursor
    url: "cursor://vscode-remote/ssh-remote+{alias}/home/dev"
    icon: cursor
  - name: PyCharm
    url: "jetbrains-gateway://connect#type=ssh&host={alias}&port=22&user=dev&projectPath=/home/dev"
    icon: pycharm
  - name: JupyterLab
    url: "/api/workspaces/{name}/lab/"
    icon: jupyterlab
    newTab: true
  - name: Internal docs
    url: "https://docs.example.com/workspaces/{name}"
    icon: external
    newTab: true
```

Reload the template catalog (local directory watcher, ConfigMap watcher, or
`helm upgrade` of the templates chart). The buttons appear on running workspaces
created from that template. There is no global Links menu or API.

## Placeholders

URLs are expanded in the browser for each workspace:

| Placeholder | Value |
|-------------|-------|
| `{name}` | Workspace name |
| `{alias}` | SSH alias, `poddy-ssh-<name>` (or `poddy-ssh-<region>-<name>`) |
| `{sshHost}` | Public SSH host |
| `{sshPort}` | Public SSH port |
| `{user}` | SSH user, `dev` |
| `{template}` | Template name |
| `{image}` | Workspace image |

Example Cursor link:

```text
cursor://vscode-remote/ssh-remote+{alias}/home/dev
```

Example PyCharm link (adjust the query parameters to your Gateway version):

```text
jetbrains-gateway://connect#type=ssh&host={alias}&port=22&user=dev&projectPath=/home/dev
```

JetBrains Gateway does not support `ProxyJump`. Copy the **SSH** config from the
portal first and enable **Parse config file ~/.ssh/config** in Gateway; the
generated `Host poddy-ssh-*` block uses `ProxyCommand`. Gateway opens its
new-connection wizard so the user can choose the IDE version to install on the
workspace.

The built-in **VS Code** and **Cursor** buttons are separate: in local editor
mode they write the SSH config and start the local port-forward before launching
the editor in a new window; in cluster mode they open a short-lived signed
`vscode://` or `cursor://slimaineb.poddy-remote/...` URL. The Poddy extension
enrolls the machine key and writes the SSH config automatically, so no manual
`~/.ssh/config` edit is required. The **PyCharm** button opens JetBrains Gateway
with the generated SSH alias. The **JupyterLab** button opens `/lab/` through the
portal proxy in a new tab; it is shown only when the workspace has the
`jupyterlab` sidecar. The **SSH** dialog remains available as a manual fallback.
When browser VS Code is enabled (`browserVSCode`), the **VS Code Web** button
opens `/home/dev` in a new browser tab through the portal proxy.

## Built-in IDE registry

Built-in IDE buttons are declared in `web/ides.js`. Each entry defines its
label, icon, launch kind, availability, and URL builder:

- `desktop`: VS Code-style editors. Local mode uses `localPath`; cluster mode
  posts to `/api/workspaces/{name}/desktop?editor=...`.
- `gateway`: JetBrains Gateway IDEs such as PyCharm. Local mode writes the SSH
  config through `/api/workspaces/{name}/gateway`; cluster mode opens the
  `jetbrains-gateway://` URL directly.
- `browser`: web IDEs. The entry provides a URL and optionally a `port` to check
  before opening a new tab.

The built-in **JupyterLab** entry is available when `workspace.jupyterLab` is
true. The Python template enables the `jupyterLab` parameter by default; other
templates can opt in with the same parameter.

## Share a workspace port

The **Ports** dialog can turn a listening port into a shareable link. Each port
has three access modes:

| Mode | Who can open the link |
|------|-----------------------|
| **Private** | Only the workspace owner and admins, through the normal portal session. No share link exists. |
| **Internal** | Any signed-in portal user. The link redirects to the portal login when needed. |
| **Public** | Anyone with the link, without a portal login. |

Select the mode in the Ports dialog, then use **Copy link**. The link looks like
`/share/<token>` on a standalone portal, or `/share/<region>/<token>` on a
central hub. The portal sets a short-lived, path-scoped cookie and redirects to
the application. Subresources and WebSocket traffic use the same cookie.

For a workspace with a built-in browser IDE, sharing its port creates a link
to the IDE route instead of the generic port proxy: port `8888` uses `/lab/`
when JupyterLab is enabled, and port `3000` uses `/code/` when VS Code Web is
enabled. This keeps the IDE's expected base path.

Share links are stored in PostgreSQL `share_links` with a SHA-256 lookup hash and
an encrypted raw token; the `poddy-shares` Secret is only an import source. They survive workspace stop/start, are removed when the workspace is
deleted, and are revoked immediately when the port is set back to **Private**.
Treat a public link as a bearer credential: anyone who has it can reach the
application until you revoke it.

To add an IDE, add its SVG symbol to `web/index.html`, then add one entry to
`IDE_INTEGRATIONS`. No card or SSH-dialog markup needs to change.

## Fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | Button label, 1-64 printable characters, unique within the template. |
| `url` | yes | Absolute URL, path starting with `/`, or an app protocol such as `jetbrains-gateway:`. |
| `icon` | no | `external` (default), `vscode`, `cursor`, `pycharm`, `jetbrains`, `jupyterlab`, `terminal`, `code`, `link`. |
| `newTab` | no | Open in a new browser tab with `noopener noreferrer`. |

## Validation and security

The server validates links when the template is loaded. It rejects:

- `javascript:`, `data:`, and `vbscript:` URLs;
- whitespace, control characters, and malformed absolute URLs;
- duplicate link names within one template.

Template authors control the links. Only users who can edit a template (global
admins or template owners) can change them.
