"use strict";

function renderCard(workspace) {
  const card = document.createElement("article");
  card.className = "card";

  const top = document.createElement("div");
  top.className = "card-top";

  const title = document.createElement("div");
  title.className = "card-title";
  const name = document.createElement("h2");
  name.textContent = workspace.name;
  name.title = workspace.name;
  title.append(name);

  const status = document.createElement("span");
  status.className = "status";
  const dot = document.createElement("span");
  dot.className = `dot ${statusClass(workspace.phase)}`;
  status.append(dot, document.createTextNode(workspace.phase || "Unknown"));

  const remove = document.createElement("button");
  remove.type = "button";
  remove.className = "icon-button danger";
  remove.title = "Delete workspace";
  remove.setAttribute("aria-label", `Delete ${workspace.name}`);
  remove.innerHTML = icon("trash");
  remove.addEventListener("click", () => confirmDelete(workspace));

  const details = document.createElement("button");
  details.type = "button";
  details.className = "icon-button info";
  details.title = "Workspace details";
  details.setAttribute("aria-label", `Details for ${workspace.name}`);
  details.innerHTML = icon("info");
  details.addEventListener("click", () => showDetails(workspace));

  const topRight = document.createElement("div");
  topRight.className = "card-top-right";
  topRight.append(status, details, remove);

  top.append(title, topRight);

  const meta = document.createElement("div");
  meta.className = "card-meta";

  const metaRow = document.createElement("div");
  metaRow.className = "card-meta-row";

  const template = document.createElement("span");
  template.className = "badge neutral";
  template.textContent = workspace.template || "default";
  metaRow.append(template);

  if (state.me.isAdmin && workspace.owner) {
    const owner = document.createElement("span");
    owner.className = "badge owner";
    owner.textContent = workspace.owner;
    owner.title = `Owner: ${workspace.owner}`;
    metaRow.append(owner);
  }

  const created = document.createElement("span");
  created.textContent = workspace.createdAt ? `Created ${formatAge(workspace.createdAt)}` : "";

  meta.append(metaRow, created);

  card.append(top, meta, renderWorkspaceActions(workspace));
  return card;
}

function renderWorkspaceActions(workspace) {
  const actions = document.createElement("div");
  actions.className = "card-actions";
  const stopped = workspace.phase === "Stopped";
  const running = workspace.phase === "Running";

  if (!stopped) {
    if (state.config.localVSCode || state.config.ssh) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "button small vscode";
      button.innerHTML = `${icon("vscode", "icon brand-icon")}VS Code`;
      button.disabled = !running;
      if (!running) {
        button.title = "Workspace is not running";
      } else if (state.config.localVSCode) {
        button.title = "Open VS Code on the portal machine";
      } else {
        button.title = "Open Remote-SSH; requires the SSH config from the SSH button";
      }
      button.addEventListener("click", () => {
        if (state.config.localVSCode) {
          launchVSCode(workspace, button);
        } else {
          window.location.href = vscodeURL(workspace.name);
        }
      });
      actions.append(button);
    }

    if (state.config.ssh) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "button small ssh";
      button.innerHTML = `${icon("key")}SSH`;
      button.addEventListener("click", () => showSSH(workspace));
      actions.append(button);
    }

    const terminal = document.createElement("a");
    terminal.className = "button small terminal";
    terminal.href = withToken(`/terminal?name=${encodeURIComponent(workspace.name)}`);
    terminal.innerHTML = `${icon("terminal")}Terminal`;
    if (!running) {
      terminal.setAttribute("aria-disabled", "true");
      terminal.title = "Workspace is not running";
      terminal.tabIndex = -1;
      terminal.style.opacity = "0.45";
      terminal.style.pointerEvents = "none";
      terminal.addEventListener("click", (event) => event.preventDefault());
    }
    actions.append(terminal);

    const template = templateByName(workspace.template);
    for (const link of (template && Array.isArray(template.links) ? template.links : [])) {
      const href = resolveLink(link, workspace);
      if (!href) continue;
      const anchor = document.createElement("a");
      anchor.className = "button small link-button";
      anchor.href = href;
      const iconName = ["vscode", "jetbrains", "terminal", "code", "link", "external"].includes(link.icon) ? link.icon : "external";
      const iconClass = iconName === "vscode" || iconName === "jetbrains" ? "icon brand-icon" : "icon";
      anchor.innerHTML = `${icon(iconName, iconClass)}<span></span>`;
      anchor.querySelector("span").textContent = link.name;
      if (link.newTab) {
        anchor.target = "_blank";
        anchor.rel = "noopener noreferrer";
      }
      actions.append(anchor);
    }
  }

  if (stopped) {
    const start = document.createElement("button");
    start.type = "button";
    start.className = "button small start";
    start.innerHTML = `${icon("play")}Start`;
    start.addEventListener("click", () => runWorkspaceAction(workspace, "start", start));
    actions.append(start);
  } else {
    const restart = document.createElement("button");
    restart.type = "button";
    restart.className = "button small restart";
    restart.innerHTML = `${icon("refresh")}Restart`;
    restart.addEventListener("click", () => confirmWorkspaceAction(workspace, "restart"));
    actions.append(restart);

    const stop = document.createElement("button");
    stop.type = "button";
    stop.className = "button small stop";
    stop.innerHTML = `${icon("stop")}Stop`;
    stop.addEventListener("click", () => confirmWorkspaceAction(workspace, "stop"));
    actions.append(stop);
  }

  return actions;
}
function workspaceMatches(workspace, query) {
  if (!query) return true;
  const haystack = [workspace.name, workspace.template, workspace.owner, workspace.phase, workspace.image]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
  return haystack.includes(query);
}

function filteredWorkspaces() {
  const query = state.workspaceQuery.trim().toLowerCase();
  return [...state.workspaces]
    .filter((workspace) => workspaceMatches(workspace, query))
    .sort((a, b) => a.name.localeCompare(b.name));
}

function syncWorkspaceViewButtons() {
  const list = state.workspaceView === "list";
  els.workspaceViewCards.classList.toggle("active", !list);
  els.workspaceViewList.classList.toggle("active", list);
  els.workspaceViewCards.setAttribute("aria-pressed", String(!list));
  els.workspaceViewList.setAttribute("aria-pressed", String(list));
}

function setWorkspaceView(view) {
  state.workspaceView = view === "list" ? "list" : "cards";
  try {
    localStorage.setItem("poddy.workspaceView", state.workspaceView);
  } catch {
    // ignore storage failures
  }
  renderWorkspaces();
}

function renderWorkspaces() {
  els.loading.hidden = true;
  syncWorkspaceViewButtons();

  const workspaces = filteredWorkspaces();
  const total = state.workspaces.length;
  const query = state.workspaceQuery.trim();
  els.count.textContent = query
    ? `${workspaces.length} of ${total} workspace${total === 1 ? "" : "s"}`
    : `${total} workspace${total === 1 ? "" : "s"}`;

  els.grid.replaceChildren();
  els.workspaceList.replaceChildren();
  els.grid.hidden = true;
  els.workspaceListWrap.hidden = true;
  els.empty.hidden = true;

  if (!workspaces.length) {
    els.empty.hidden = false;
    if (query) {
      els.emptyTitle.textContent = "No matching workspaces";
      els.emptyText.textContent = "Try a different name, template, owner, or status.";
    } else {
      els.emptyTitle.textContent = "No workspaces yet";
      els.emptyText.textContent = "Create your first development environment from a template.";
    }
    return;
  }

  const fragment = document.createDocumentFragment();
  if (state.workspaceView === "list") {
    for (const workspace of workspaces) fragment.append(renderWorkspaceRow(workspace));
    els.workspaceList.append(fragment);
    els.workspaceListWrap.hidden = false;
  } else {
    for (const workspace of workspaces) fragment.append(renderCard(workspace));
    els.grid.append(fragment);
    els.grid.hidden = false;
  }
}

function renderWorkspaceRow(workspace) {
  const row = document.createElement("tr");

  const nameCell = document.createElement("td");
  const nameButton = document.createElement("button");
  nameButton.type = "button";
  nameButton.className = "workspace-name-button";
  nameButton.textContent = workspace.name;
  nameButton.title = workspace.name;
  nameButton.addEventListener("click", () => showDetails(workspace));
  nameCell.append(nameButton);

  const statusCell = document.createElement("td");
  const status = document.createElement("span");
  status.className = "status";
  const dot = document.createElement("span");
  dot.className = `dot ${statusClass(workspace.phase)}`;
  status.append(dot, document.createTextNode(workspace.phase || "Unknown"));
  statusCell.append(status);

  const templateCell = document.createElement("td");
  const template = document.createElement("span");
  template.className = "badge neutral";
  template.textContent = workspace.template || "default";
  templateCell.append(template);

  const ownerCell = document.createElement("td");
  ownerCell.className = "workspace-owner";
  ownerCell.textContent = workspace.owner || "";
  ownerCell.title = workspace.owner || "";

  const createdCell = document.createElement("td");
  createdCell.className = "workspace-created";
  createdCell.textContent = workspace.createdAt ? formatAge(workspace.createdAt) : "";

  const actionsCell = document.createElement("td");
  actionsCell.className = "workspace-table-actions";
  const actions = renderWorkspaceActions(workspace);
  actions.classList.add("workspace-actions");

  const remove = document.createElement("button");
  remove.type = "button";
  remove.className = "icon-button danger";
  remove.title = "Delete workspace";
  remove.setAttribute("aria-label", `Delete ${workspace.name}`);
  remove.innerHTML = icon("trash");
  remove.addEventListener("click", () => confirmDelete(workspace));
  actions.append(remove);
  actionsCell.append(actions);

  row.append(nameCell, statusCell, templateCell, ownerCell, createdCell, actionsCell);
  return row;
}
async function load({ silent = false } = {}) {
  if (!silent && !state.loaded) els.loading.hidden = false;

  try {
    const workspaces = await api("/api/workspaces");
    state.workspaces = Array.isArray(workspaces) ? workspaces : [];
    state.loaded = true;
    state.pollError = false;
    renderWorkspaces();
  } catch (err) {
    if (!silent || !state.pollError) {
      toast(err.message || "Failed to load workspaces.");
      state.pollError = true;
    }
  }
}
function workspaceNamePrefix() {
  if (!state.me || state.me.method === "none") return "";
  let raw = state.me.username || "";
  if (!raw && state.me.email) raw = state.me.email.split("@")[0];
  raw = String(raw).toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 32).replace(/-+$/g, "");
  return raw || "user";
}

function renderWorkspaceNameHint() {
  const prefix = workspaceNamePrefix();
  if (!prefix) {
    els.nameInput.placeholder = "my-workspace";
    els.nameHint.textContent = "Lowercase letters, numbers, and dashes.";
    return;
  }
  els.nameInput.placeholder = `${prefix}-my-workspace`;
  els.nameHint.textContent = `Lowercase letters, numbers, and dashes. Your username is added automatically: ${prefix}-my-workspace.`;
}

async function openCreate() {
  if (!state.templates.length) {
    toast("No templates are available. Check the portal configuration.");
    return;
  }
  els.createForm.reset();
  setFormError(els.createError, "");
  renderWorkspaceNameHint();
  els.params.replaceChildren();
  if (!els.templateSelect.value) {
    els.templateSelect.value = state.templates[0].name;
  }
  await loadExternalAuth();
  renderParams();
  els.createDialog.showModal();
  requestAnimationFrame(() => els.nameInput.focus());
}
function detailRow(label, value, { mono = false } = {}) {
  const row = document.createElement("div");
  row.className = "detail-row";
  const key = document.createElement("dt");
  key.textContent = label;
  const val = document.createElement("dd");
  if (mono) val.className = "mono";
  if (value instanceof Node) {
    val.append(value);
  } else {
    val.textContent = value;
  }
  row.append(key, val);
  return row;
}
function linkValue(workspace, key) {
  switch (key) {
    case "name":
      return workspace.name;
    case "alias":
      return `poddy-${workspace.name}`;
    case "sshHost":
      return state.config.sshHost || "127.0.0.1";
    case "sshPort":
      return state.config.sshPort || "2222";
    case "user":
      return "dev";
    case "template":
      return workspace.template || "";
    case "image":
      return workspace.image || "";
    default:
      return "";
  }
}
function resolveLink(link, workspace) {
  return String(link.url || "").replace(/\{([a-zA-Z]+)\}/g, (match, key) => {
    const value = linkValue(workspace, key);
    return value === "" ? match : value;
  });
}
async function showDetails(workspace) {
  els.detailTitle.textContent = workspace.name;
  els.detailList.replaceChildren();

  // Show loading
  const loading = document.createElement("div");
  loading.className = "loading-small";
  loading.textContent = "Loading details...";
  els.detailList.append(loading);
  els.detailDialog.showModal();

  try {
    const res = await api(`/api/workspaces/${encodeURIComponent(workspace.name)}`);
    renderDetails(res);
  } catch (err) {
    toast(err.message || "Failed to load details");
    renderDetails(workspace);
  }
}
function renderDetails(ws) {
  els.detailList.replaceChildren();

  const status = document.createElement("span");
  status.className = "status";
  const dot = document.createElement("span");
  dot.className = `dot ${statusClass(ws.phase)}`;
  status.append(dot, document.createTextNode(ws.phase || "Unknown"));

  els.detailList.append(detailRow("Status", status));
  els.detailList.append(detailRow("Template", ws.template || "default"));
  if (ws.image) {
    els.detailList.append(detailRow("Image", ws.image, { mono: true }));
  }
  if (ws.owner) {
    els.detailList.append(detailRow("Owner", ws.owner));
  }
  if (ws.podIP) {
    els.detailList.append(detailRow("Pod IP", ws.podIP, { mono: true }));
  }
  if (ws.nodeName) {
    els.detailList.append(detailRow("Node", ws.nodeName));
  }
  if (ws.podPhase) {
    els.detailList.append(detailRow("Pod Phase", ws.podPhase));
  }
  els.detailList.append(detailRow("Storage", ws.persistent ? "Persistent volume" : "Ephemeral"));
  if (ws.createdAt) {
    const date = new Date(ws.createdAt);
    const value = Number.isNaN(date.getTime())
      ? ws.createdAt
      : `${date.toLocaleString()} (${formatAge(ws.createdAt)})`;
    els.detailList.append(detailRow("Created", value));
  }

  // Access control
  if (ws.owners && ws.owners.length) {
    els.detailList.append(detailRow("Owners", ws.owners.join(", ")));
  }
  if (ws.users && ws.users.length) {
    els.detailList.append(detailRow("Users", ws.users.join(", ")));
  }
  if (ws.teams && ws.teams.length) {
    const teamsEl = document.createElement("div");
    for (const team of ws.teams) {
      const teamRow = document.createElement("div");
      teamRow.style.marginLeft = "1em";
      teamRow.textContent = `${team}`;
      if (ws.teamMembers && ws.teamMembers[team]) {
        teamRow.textContent += ` (${ws.teamMembers[team].join(", ")})`;
      }
      teamsEl.append(teamRow);
    }
    els.detailList.append(detailRow("Teams", teamsEl));
  }
}
function showSSH(workspace) {
  state.sshWorkspace = workspace;
  els.sshConfig.textContent = sshConfigFor();
  const windowsClient = /Windows/i.test(navigator.userAgent);
  if (state.config.windows) {
    els.sshNote.textContent = `The portal key is installed on Windows at ${state.config.windowsSSHKeyPath || "~/.ssh/poddy_id_ed25519"}. This config covers all workspaces.`;
  } else if (windowsClient) {
    els.sshNote.textContent = "Save this config once in C:\\Users\\<you>\\.ssh\\config and copy your private key to the same .ssh folder. It covers all workspaces.";
  } else if (state.config.sshKeyPath) {
    els.sshNote.textContent = `Save this config once in ~/.ssh/config and use the key at ${state.config.sshKeyPath}. It covers all workspaces.`;
  } else {
    els.sshNote.textContent = "Save this config once in ~/.ssh/config. It covers all workspaces.";
  }
  els.sshDialog.showModal();
}
function confirmWorkspaceAction(workspace, action) {
  let message;
  if (action === "stop") {
    message = workspace.persistent
      ? `Stop "${workspace.name}"? The pod is deleted and its volume is kept.`
      : `Stop "${workspace.name}"? This workspace has no persistent volume; its data will be lost.`;
  } else {
    message = `Restart "${workspace.name}"? The pod is recreated and its volume is kept.`;
  }
  if (window.confirm(message)) {
    runWorkspaceAction(workspace, action);
  }
}
async function runWorkspaceAction(workspace, action, element) {
  const original = element ? element.innerHTML : "";
  if (element) {
    element.disabled = true;
    element.setAttribute("aria-busy", "true");
    element.innerHTML = `<span class="spinner small"></span>${action === "stop" ? "Stopping" : action === "start" ? "Starting" : "Restarting"}`;
  }
  try {
    await api(`/api/workspaces/${encodeURIComponent(workspace.name)}/${action}`, { method: "POST" });
    const done = action === "stop" ? "stopped" : action === "start" ? "started" : "restarting";
    toast(`Workspace ${workspace.name} ${done}.`, "success");
    await load();
  } catch (err) {
    toast(err.message || `Failed to ${action} workspace.`);
  } finally {
    if (element) {
      element.disabled = false;
      element.removeAttribute("aria-busy");
      element.innerHTML = original;
    }
  }
}
function confirmDelete(workspace) {
  state.pendingDelete = workspace;
  els.confirmText.textContent = `Delete "${workspace.name}"? This removes the pod and its persistent volume.`;
  els.confirmDialog.showModal();
}
async function launchVSCode(workspace, element) {
  const original = element.innerHTML;
  element.disabled = true;
  element.setAttribute("aria-busy", "true");
  element.innerHTML = `<span class="spinner small"></span>Opening`;

  try {
    await api(`/api/workspaces/${encodeURIComponent(workspace.name)}/vscode`, { method: "POST" });
    toast(`VS Code is opening ${workspace.name}.`, "success");
  } catch (err) {
    toast(err.message || "Failed to open VS Code.");
  } finally {
    element.disabled = false;
    element.removeAttribute("aria-busy");
    element.innerHTML = original;
  }
}
async function copySSHConfig() {
  const text = els.sshConfig.textContent;
  try {
    await navigator.clipboard.writeText(text);
    toast("SSH config copied.", "success");
  } catch {
    const range = document.createRange();
    range.selectNodeContents(els.sshConfig);
    const selection = window.getSelection();
    selection.removeAllRanges();
    selection.addRange(range);
    toast("Press Ctrl+C to copy the SSH config.");
  }
}
