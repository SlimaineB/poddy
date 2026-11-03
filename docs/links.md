# Template links

Links are buttons shown on workspace cards. They are declared in the workspace
template, so each template controls its own shortcuts and they are versioned
with it. Use them for JetBrains Gateway, internal dashboards, documentation, or
any protocol the browser can open.

## Add links to a template

Add a `links` list to `template.yaml`:

```yaml
name: go
displayName: Go
image: poddy/workspace:latest
links:
  - name: JetBrains Gateway
    url: "jetbrains-gateway://connect#type=ssh&host={sshHost}&port={sshPort}&user={user}&project=/workspace"
    icon: jetbrains
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
| `{alias}` | SSH alias, `poddy-<name>` |
| `{sshHost}` | Public SSH host |
| `{sshPort}` | Public SSH port |
| `{user}` | SSH user, `dev` |
| `{template}` | Template name |
| `{image}` | Workspace image |

Example JetBrains Gateway link (adjust the query parameters to your Gateway version):

```text
jetbrains-gateway://connect#type=ssh&host={sshHost}&port={sshPort}&user={user}&project=/workspace
```

The built-in **VS Code** button is separate: in local editor mode it writes the SSH config and starts the local port-forward before launching VS Code; in cluster mode it opens `vscode://vscode-remote/ssh-remote+.../home/dev` and requires the SSH config from the **SSH** dialog. That config uses `Host poddy-ssh-*`, so one entry covers all workspaces.

## Fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | Button label, 1-64 printable characters, unique within the template. |
| `url` | yes | Absolute URL, path starting with `/`, or an app protocol such as `jetbrains-gateway:`. |
| `icon` | no | `external` (default), `vscode`, `jetbrains`, `terminal`, `code`, `link`. |
| `newTab` | no | Open in a new browser tab with `noopener noreferrer`. |

## Validation and security

The server validates links when the template is loaded. It rejects:

- `javascript:`, `data:`, and `vbscript:` URLs;
- whitespace, control characters, and malformed absolute URLs;
- duplicate link names within one template.

Template authors control the links. Only users who can edit a template (global
admins or template owners) can change them.
