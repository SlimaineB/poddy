"use strict";

const IDE_INTEGRATIONS = [
  {
    id: "vscode",
    label: "VS Code",
    icon: "vscode",
    brand: true,
    kind: "desktop",
    localFlag: "localVSCode",
    remoteEditor: "vscode",
    buttonClass: "vscode",
    localPath: (workspace) => `/api/workspaces/${encodeURIComponent(workspace.name)}/vscode`,
    title: (local) => local ? "Open VS Code on the portal machine" : "Open VS Code Desktop (installs the Poddy extension once)",
  },
  {
    id: "cursor",
    label: "Cursor",
    icon: "cursor",
    brand: true,
    kind: "desktop",
    localFlag: "localCursor",
    remoteEditor: "cursor",
    buttonClass: "cursor",
    localPath: (workspace) => `/api/workspaces/${encodeURIComponent(workspace.name)}/cursor`,
    title: (local) => local ? "Open Cursor on the portal machine" : "Open Cursor Desktop (installs the Poddy extension once)",
  },
  {
    id: "pycharm",
    label: "PyCharm",
    icon: "pycharm",
    brand: true,
    kind: "gateway",
    localFlag: "localMode",
    buttonClass: "pycharm",
    localPath: (workspace) => `/api/workspaces/${encodeURIComponent(workspace.name)}/gateway`,
    url: (workspace) => gatewayURL(workspace),
    title: (local) => local ? "Open PyCharm through JetBrains Gateway on the portal machine" : "Open PyCharm through JetBrains Gateway (uses the Poddy SSH config)",
  },
  {
    id: "jupyterlab",
    label: "JupyterLab",
    icon: "jupyterlab",
    brand: true,
    kind: "browser",
    buttonClass: "jupyterlab",
    port: 8888,
    url: (workspace) => withToken(`/api/workspaces/${encodeURIComponent(workspace.name)}/ports/8888/lab`),
    title: () => "Open JupyterLab in a new tab (port 8888)",
  },
  {
    id: "vscode-web",
    label: "VS Code Web",
    icon: "vscode",
    brand: true,
    kind: "browser",
    buttonClass: "vscode",
    available: (workspace) => Boolean(workspace.browserVSCode),
    url: (workspace) => withToken(browserVSCodeURL(workspace.name)),
    title: () => "Open VS Code in a new browser tab",
  },
];

function ideUsesLocalLaunch(ide) {
  if (ide.kind === "desktop") return Boolean(state.config[ide.localFlag]);
  if (ide.kind === "gateway") return Boolean(state.config.localMode);
  return false;
}

function ideAvailable(ide, workspace) {
  if (ide.available && !ide.available(workspace)) return false;
  if (ide.kind === "browser") return true;
  if (ide.kind === "desktop") {
    return state.config.localMode ? Boolean(state.config[ide.localFlag]) : Boolean(state.config.ssh);
  }
  if (ide.kind === "gateway") return Boolean(state.config.localMode || state.config.ssh);
  return false;
}

async function openIDE(ide, workspace, element) {
  if (ide.kind === "browser") {
    const target = ide.port ? window.open("about:blank", "_blank") : null;
    if (target) target.opener = null;
    try {
      if (ide.port) {
        const data = await api(`/api/workspaces/${encodeURIComponent(workspace.name)}/ports`);
        const ports = Array.isArray(data.ports) ? data.ports : [];
        if (!ports.some((item) => Number(item.port) === ide.port)) {
          if (target) target.close();
          toast(`${ide.label} is not listening on port ${ide.port} in this workspace.`);
          return;
        }
      }
      const url = ide.url(workspace);
      if (target) target.location.href = url;
      else window.open(url, "_blank", "noopener");
    } catch (err) {
      if (target) target.close();
      toast(err.message || `Failed to open ${ide.label}.`);
    }
    return;
  }

  const local = ideUsesLocalLaunch(ide);
  const original = element.innerHTML;
  element.disabled = true;
  element.setAttribute("aria-busy", "true");
  element.innerHTML = `<span class="spinner small"></span>Opening`;

  try {
    if (local) {
      if (ide.kind === "desktop") {
        await api(ide.localPath(workspace), { method: "POST" });
        toast(`${ide.label} is opening ${workspace.name}.`, "success");
      } else {
        const data = await api(ide.localPath(workspace), { method: "POST" });
        if (!data || !data.url) throw new Error(`${ide.label} launch is not configured`);
        window.location.href = data.url;
      }
    } else if (ide.kind === "desktop") {
      const data = await api(`/api/workspaces/${encodeURIComponent(workspace.name)}/desktop?editor=${encodeURIComponent(ide.remoteEditor)}`, { method: "POST" });
      if (!data || !data.url) throw new Error(`${ide.label} launch is not configured`);
      window.location.href = data.url;
    } else {
      window.location.href = ide.url(workspace);
    }
  } catch (err) {
    toast(err.message || `Failed to open ${ide.label}.`);
  } finally {
    element.disabled = false;
    element.removeAttribute("aria-busy");
    element.innerHTML = original;
  }
}

function ideButton(ide, workspace, { dialog = false } = {}) {
  const running = workspace.phase === "Running";
  const local = ideUsesLocalLaunch(ide);
  const isLink = ide.kind === "browser";
  const element = document.createElement(isLink ? "a" : "button");
  element.dataset.ide = ide.id;
  element.className = dialog ? `button ghost ${ide.buttonClass || ""}`.trim() : `button small ${ide.buttonClass || ""}`.trim();
  element.innerHTML = `${icon(ide.icon, ide.brand ? "icon brand-icon" : "icon")}${ide.label}`;
  element.title = ide.title(local);

  if (isLink) {
    element.href = ide.url(workspace);
    element.target = "_blank";
    element.rel = "noopener";
  } else {
    element.type = "button";
  }

  if (!running) {
    element.title = "Workspace is not running";
    element.setAttribute("aria-disabled", "true");
    element.tabIndex = -1;
    if (isLink) {
      element.style.opacity = "0.45";
      element.style.pointerEvents = "none";
      element.addEventListener("click", (event) => event.preventDefault());
    } else {
      element.disabled = true;
    }
    return element;
  }

  element.addEventListener("click", (event) => {
    if (dialog && els.sshDialog) els.sshDialog.close();
    if (isLink && ide.port) {
      event.preventDefault();
      openIDE(ide, workspace, element);
    } else if (!isLink) {
      openIDE(ide, workspace, element);
    }
  });
  return element;
}

function renderWorkspaceIDEButtons(container, workspace) {
  for (const ide of IDE_INTEGRATIONS) {
    if (!ideAvailable(ide, workspace)) continue;
    container.append(ideButton(ide, workspace));
  }
}

function renderSSHIDEActions(workspace) {
  if (!els.sshIDEActions) return;
  els.sshIDEActions.replaceChildren();
  for (const ide of IDE_INTEGRATIONS) {
    if (!ideAvailable(ide, workspace)) continue;
    els.sshIDEActions.append(ideButton(ide, workspace, { dialog: true }));
  }
}
