"use strict";

function renderCard(workspace) {
  const card = document.createElement("article");
  card.className = `card card-${statusClass(workspace.phase) || "unknown"}`;

  const top = document.createElement("div");
  top.className = "card-top";

  const title = document.createElement("div");
  title.className = "card-title";
  const mark = document.createElement("span");
  mark.className = "workspace-mark";
  mark.innerHTML = icon("box");
  const name = document.createElement("h2");
  const nameButton = document.createElement("button");
  nameButton.type = "button";
  nameButton.className = "workspace-name-button";
  nameButton.textContent = workspace.name;
  nameButton.title = workspace.name;
  nameButton.addEventListener("click", () => showDetails(workspace));
  name.append(nameButton);
  title.append(mark, name);

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
  template.innerHTML = `${icon("layers")}<span></span>`;
  template.querySelector("span").textContent = workspace.template || "default";
  metaRow.append(template);

  if (workspace.region) {
    const region = document.createElement("span");
    region.className = "badge neutral";
    region.innerHTML = `${icon("globe")}<span></span>`;
    region.querySelector("span").textContent = workspace.region;
    region.title = `Region: ${workspace.region}`;
    metaRow.append(region);
  }

  const schedule = scheduleSummary(workspace.schedule);
  if (schedule) {
    const badge = document.createElement("span");
    badge.className = "badge schedule";
    badge.innerHTML = `${icon("clock")}<span>Auto-stop</span>`;
    badge.title = `Auto-stop: ${schedule}`;
    metaRow.append(badge);
  }

  if (state.me.isAdmin && workspace.owner) {
    const owner = document.createElement("span");
    owner.className = "badge owner";
    owner.innerHTML = `${icon("user")}<span></span>`;
    owner.querySelector("span").textContent = workspace.owner;
    owner.title = `Owner: ${workspace.owner}`;
    metaRow.append(owner);
  }

  meta.append(metaRow);
  if (workspace.createdAt) {
    const created = document.createElement("span");
    created.className = "card-created";
    created.innerHTML = `${icon("clock")}<span></span>`;
    created.querySelector("span").textContent = `Created ${formatAge(workspace.createdAt)}`;
    meta.append(created);
  }

  card.append(top, meta, renderWorkspaceActions(workspace));
  card.classList.add("card-clickable");
  card.addEventListener("click", (event) => {
    if (event.target.closest("button, a, input, select, textarea")) return;
    showDetails(workspace);
  });
  return card;
}

function renderWorkspaceActions(workspace) {
  const actions = document.createElement("div");
  actions.className = "card-actions";
  const stopped = workspace.phase === "Stopped";
  const running = workspace.phase === "Running";

  if (!stopped) {
    renderWorkspaceIDEButtons(actions, workspace);

    if (state.config.ssh) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "button small ssh";
      button.innerHTML = `${icon("key")}SSH`;
      button.addEventListener("click", () => showSSH(workspace));
      actions.append(button);
    }

    const ports = document.createElement("button");
    ports.type = "button";
    ports.className = "button small";
    ports.innerHTML = `${icon("server")}Ports`;
    ports.title = "Open an application running in this workspace";
    ports.addEventListener("click", () => showPorts(workspace));
    actions.append(ports);

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
      const iconName = ["vscode", "cursor", "pycharm", "jetbrains", "jupyterlab", "terminal", "code", "link", "external"].includes(link.icon) ? link.icon : "external";
      const iconClass = ["vscode", "cursor", "pycharm", "jetbrains", "jupyterlab"].includes(iconName) ? "icon brand-icon" : "icon";
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
  const haystack = [workspace.name, workspace.template, workspace.region, workspace.owner, workspace.phase, workspace.image]
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
  const active = state.mapActive ? "map" : state.topologyActive ? "topology" : state.workspaceView;
  for (const button of document.querySelectorAll("[data-workspace-view]")) {
    const selected = button.dataset.workspaceView === active;
    button.classList.toggle("active", selected);
    button.setAttribute("aria-pressed", String(selected));
  }
}

function setWorkspaceView(view) {
  if (view === "topology") {
    state.topologyActive = true;
    state.mapActive = false;
    if (typeof openTopology === "function") openTopology();
    return;
  }
  if (view === "map") {
    state.topologyActive = false;
    state.mapActive = true;
    if (typeof openMap === "function") openMap();
    return;
  }
  state.topologyActive = false;
  state.mapActive = false;
  state.workspaceView = view === "list" ? "list" : "cards";
  try {
    localStorage.setItem("poddy.workspaceView", state.workspaceView);
  } catch {
    // ignore storage failures
  }
  showView("workspaces");
  renderWorkspaces();
}

for (const button of document.querySelectorAll("[data-workspace-view]")) {
  button.addEventListener("click", () => setWorkspaceView(button.dataset.workspaceView));
}

function renderWorkspaces() {
  els.loading.hidden = true;
  syncWorkspaceViewButtons();

  const workspaces = filteredWorkspaces();
  const total = state.workspaceTotal || state.workspaces.length;
  const query = state.workspaceQuery.trim();
  const regionLabel = state.regionFilter ? ` in ${regionDisplayName(state.regionFilter)}` : "";
  els.count.textContent = query
    ? `${total} matching workspace${total === 1 ? "" : "s"}${regionLabel}`
    : `${total} workspace${total === 1 ? "" : "s"}${regionLabel}`;

  els.grid.replaceChildren();
  els.workspaceList.replaceChildren();
  els.grid.hidden = true;
  els.workspaceListWrap.hidden = true;
  els.empty.hidden = true;

  if (!workspaces.length) {
    els.empty.hidden = false;
    if (query) {
      els.emptyTitle.textContent = "No matching workspaces";
      els.emptyText.textContent = "Try a different name, template, owner, status, or region.";
    } else if (state.regionFilter) {
      els.emptyTitle.textContent = `No workspaces in ${regionDisplayName(state.regionFilter)}`;
      els.emptyText.textContent = "Create a workspace in this region or choose another region.";
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
  const nameWrap = document.createElement("div");
  nameWrap.className = "workspace-name-cell";
  const nameMark = document.createElement("span");
  nameMark.className = "workspace-mark small";
  nameMark.innerHTML = icon("box");
  const nameButton = document.createElement("button");
  nameButton.type = "button";
  nameButton.className = "workspace-name-button";
  nameButton.textContent = workspace.name;
  nameButton.title = workspace.name;
  nameButton.addEventListener("click", () => showDetails(workspace));
  nameWrap.append(nameMark, nameButton);
  nameCell.append(nameWrap);

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
    const data = await api(pageURL("/api/workspaces", state.workspacePage, state.workspacePerPage, state.workspaceQuery.trim()));
    if (Array.isArray(data)) {
      state.workspaces = data;
      state.workspaceTotal = data.length;
      state.workspacePages = data.length ? 1 : 0;
      state.workspacePage = 1;
    } else {
      state.workspaces = Array.isArray(data.items) ? data.items : [];
      state.workspaceTotal = Number(data.total) || 0;
      state.workspacePages = Number(data.pages) || 0;
      state.workspacePage = Number(data.page) || 1;
      state.workspacePerPage = Number(data.perPage) || state.workspacePerPage;
    }
    state.loaded = true;
    state.pollError = false;
    renderWorkspaces();
    if ((state.regionFilter || state.mapActive) && typeof loadRegionSummaries === "function") {
      loadRegionSummaries({ silent: true });
    }
    renderPagination(els.workspacesPagination, {
      page: state.workspacePage,
      pages: state.workspacePages,
      total: state.workspaceTotal,
      summary: `${state.workspaceTotal} workspace${state.workspaceTotal === 1 ? "" : "s"}`,
    }, (page) => {
      state.workspacePage = page;
      load({ silent: true });
    });
    if (typeof refreshTopologyIfVisible === "function") refreshTopologyIfVisible();
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
  if (state.regionFilter && configuredRegions().some((region) => region.name === state.regionFilter)) {
    els.regionSelect.value = state.regionFilter;
  }
  if (!els.templateSelect.value) {
    els.templateSelect.value = state.templates[0].name;
  }
  populateTimezones(els.scheduleTimezone, currentTimezone());
  els.scheduleStart.value = "09:00";
  els.scheduleStop.value = "18:00";
  els.scheduleEnabled.checked = false;
  syncScheduleFields(els.createForm, false);
  await loadExternalAuth();
  renderParams();
  els.createDialog.showModal();
  requestAnimationFrame(() => els.nameInput.focus());
}
const detailIcons = {
  Status: "activity",
  "Auto-stop": "clock",
  Region: "globe",
  Template: "layers",
  Image: "box",
  Owner: "user",
  "Pod IP": "network",
  Node: "server",
  "Pod Phase": "activity",
  Storage: "database",
  Volumes: "hard-drive",
  Created: "calendar",
  Owners: "shield",
  Users: "users",
  Teams: "users",
};

function detailRow(label, value, { mono = false } = {}) {
  const row = document.createElement("div");
  row.className = "detail-row";
  const key = document.createElement("dt");
  key.innerHTML = `${icon(detailIcons[label] || "info")}<span></span>`;
  key.querySelector("span").textContent = label;
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
      return sshAlias(workspace.name, workspace.region);
    case "sshHost": {
      const region = (state.config.regions || []).find((item) => item.name === workspace.region);
      return (region && region.sshHost) || state.config.sshHost || "127.0.0.1";
    }
    case "sshPort": {
      const region = (state.config.regions || []).find((item) => item.name === workspace.region);
      return (region && region.sshPort) || state.config.sshPort || "2222";
    }
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
  state.detailWorkspace = workspace;
  state.manifests = null;
  state.manifestTab = "pod";
  renderDetailHeader(workspace);
  els.detailList.replaceChildren();
  els.detailLogsBody.replaceChildren();
  els.detailManifests.hidden = !(state.me && state.me.isAdmin);
  if (!els.detailManifests.hidden) setManifestsMessage("Loading manifests...");
  els.detailPortsBody.replaceChildren();
  els.detailPortInput.value = "";

  const loading = document.createElement("div");
  loading.className = "loading-small";
  loading.textContent = "Loading details...";
  els.detailList.append(loading);
  setLogsMessage("Loading startup logs...");
  setPortsMessage(els.detailPortsBody, "Detecting listening ports...");
  if (els.workspaceDetailView.hidden) showView("workspace-detail");

  await refreshDetails();
}

function closeWorkspaceDetail() {
  state.detailWorkspace = null;
  state.detailRequest += 1;
  state.manifests = null;
  state.manifestTab = "pod";
  state.detailPortsRequest += 1;
  showView("workspaces");
  if (typeof syncWorkspaceViewButtons === "function") syncWorkspaceViewButtons();
  load({ silent: true });
}

function renderDetailHeader(workspace) {
  els.detailTitle.textContent = workspace.name || "Workspace";
  const bits = [];
  if (workspace.template) bits.push(workspace.template);
  if (workspace.region) bits.push(workspace.regionDisplayName || workspace.region);
  if (workspace.owner) bits.push(workspace.owner);
  els.detailSubtitle.textContent = bits.length
    ? bits.join(" · ")
    : "Metadata, ports, rendered manifests, and startup logs for this workspace.";

  const phase = workspace.phase || "Unknown";
  els.detailStatus.replaceChildren();
  const badge = document.createElement("span");
  badge.className = `detail-status-badge ${statusClass(phase)}`;
  badge.innerHTML = `<span class="dot ${statusClass(phase)}"></span><span></span>`;
  badge.querySelector("span:last-child").textContent = phase;
  els.detailStatus.append(badge);

  const actions = renderWorkspaceActions(workspace);
  actions.classList.add("detail-action-list");
  const remove = document.createElement("button");
  remove.type = "button";
  remove.className = "button small danger";
  remove.innerHTML = `${icon("trash")}Delete`;
  remove.addEventListener("click", () => confirmDelete(workspace));
  actions.append(remove);
  els.detailActions.replaceChildren(actions);
}

async function refreshDetails() {
  const workspace = state.detailWorkspace;
  if (!workspace) return;
  const request = ++state.detailRequest;
  const portsRequest = ++state.detailPortsRequest;
  els.detailLogsRefresh.disabled = true;
  els.detailManifestsRefresh.disabled = true;
  els.detailPortsRefresh.disabled = true;
  const jobs = [
    loadDetails(workspace, request),
    loadLogs(workspace, request),
    loadPorts(workspace, portsRequest, els.detailPortsBody, () => state.detailPortsRequest),
  ];
  if (state.me && state.me.isAdmin) jobs.push(loadManifests(workspace, request));
  await Promise.all(jobs);
  if (request === state.detailRequest) {
    els.detailLogsRefresh.disabled = false;
    els.detailManifestsRefresh.disabled = false;
  }
  if (portsRequest === state.detailPortsRequest) {
    els.detailPortsRefresh.disabled = false;
  }
}

async function refreshDetailPorts() {
  const workspace = state.detailWorkspace;
  if (!workspace) return;
  const request = ++state.detailPortsRequest;
  els.detailPortsRefresh.disabled = true;
  try {
    await loadPorts(workspace, request, els.detailPortsBody, () => state.detailPortsRequest);
  } finally {
    if (request === state.detailPortsRequest) {
      els.detailPortsRefresh.disabled = false;
    }
  }
}

function showPorts(workspace) {
  state.portsWorkspace = workspace;
  els.portsTitle.textContent = "Ports";
  els.portsSubtitle.textContent = `Applications listening in ${workspace.name}.`;
  els.portsBody.replaceChildren();
  els.portsInput.value = "";
  setPortsMessage(els.portsBody, "Detecting listening ports...");
  if (!els.portsDialog.open) els.portsDialog.showModal();
  refreshPorts();
}

async function refreshPorts() {
  const workspace = state.portsWorkspace;
  if (!workspace) return;
  const request = ++state.portsRequest;
  els.portsRefresh.disabled = true;
  try {
    await loadPorts(workspace, request, els.portsBody, () => state.portsRequest);
  } finally {
    if (request === state.portsRequest) {
      els.portsRefresh.disabled = false;
    }
  }
}

function setPortsMessage(body, message) {
  body.replaceChildren();
  const note = document.createElement("p");
  note.className = "hint";
  note.textContent = message;
  body.append(note);
}

function workspacePortURL(name, port) {
  return `/api/workspaces/${encodeURIComponent(name)}/ports/${encodeURIComponent(port)}/`;
}

async function loadPorts(workspace, request, body, currentRequest) {
  try {
    const data = await api(`/api/workspaces/${encodeURIComponent(workspace.name)}/ports`);
    if (request !== currentRequest()) return;
    renderPorts(workspace, data || {}, body);
  } catch (err) {
    if (request !== currentRequest()) return;
    setPortsMessage(body, err.message || "Could not load ports.");
  }
}

function renderPorts(workspace, data, body) {
  body.replaceChildren();
  const ports = Array.isArray(data.ports) ? data.ports : [];
  const message = String(data.message || "").trim();

  if (!ports.length) {
    setPortsMessage(body, message || "No listening TCP ports detected. Start a server in the workspace or enter a port below.");
    return;
  }

  if (message) {
    const note = document.createElement("p");
    note.className = "hint";
    note.textContent = message;
    body.append(note);
  }

  for (const item of ports) {
    const port = Number(item.port);
    if (!Number.isInteger(port) || port < 1 || port > 65535) continue;

    const row = document.createElement("div");
    row.className = "port-row";

    const main = document.createElement("div");
    main.className = "port-main";
    const number = document.createElement("span");
    number.className = "port-number";
    number.textContent = `:${port}`;
    const address = document.createElement("span");
    address.className = "port-address";
    address.textContent = [item.protocol || "tcp", item.address].filter(Boolean).join(" · ");
    main.append(number, address);

    const actions = document.createElement("div");
    actions.className = "port-actions";

    const share = document.createElement("select");
    share.className = "port-share-select";
    share.title = "Share access for this port";
    share.setAttribute("aria-label", `Share access for port ${port}`);
    for (const [value, label] of [["off", "Private"], ["internal", "Internal"], ["public", "Public"]]) {
      const option = document.createElement("option");
      option.value = value;
      option.textContent = label;
      share.append(option);
    }
    share.value = item.share?.access || "off";
    share.dataset.current = share.value;
    share.addEventListener("change", () => updatePortShare(workspace, port, share, actions));
    actions.append(share);

    if (item.share?.url) {
      actions.append(shareCopyButton(item.share.url));
    }

    const open = document.createElement("a");
    open.className = "button small";
    open.textContent = "Open";
    open.href = withToken(item.url || workspacePortURL(workspace.name, port));
    open.target = "_blank";
    open.rel = "noopener";
    open.title = `Open port ${port} in a new tab`;

    row.append(main, actions, open);
    body.append(row);
  }
}

function shareCopyButton(url) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "button ghost small port-share-copy";
  button.textContent = "Copy link";
  button.title = "Copy share link";
  button.addEventListener("click", () => copyShareURL(url));
  return button;
}

async function updatePortShare(workspace, port, select, actions) {
  const access = select.value;
  select.disabled = true;
  try {
    const data = await api(`/api/workspaces/${encodeURIComponent(workspace.name)}/ports/${encodeURIComponent(port)}/share`, {
      method: "PUT",
      body: JSON.stringify({ access }),
    });
    const share = data && data.share ? data.share : null;
    const current = actions.querySelector(".port-share-copy");
    if (current) current.remove();
    if (share?.url) actions.append(shareCopyButton(share.url));
    select.dataset.current = access;
    toast(share ? `Port ${port} share set to ${access}.` : `Port ${port} share removed.`, "success");
  } catch (err) {
    select.value = select.dataset.current || "off";
    toast(err.message || "Could not update share.");
  } finally {
    select.disabled = false;
  }
}

async function copyShareURL(url) {
  const full = new URL(url, location.origin).href;
  try {
    await navigator.clipboard.writeText(full);
    toast("Share link copied.", "success");
  } catch {
    toast(`Copy this link: ${full}`);
  }
}

function openPort(workspace, input) {
  if (!workspace) return;
  const port = Number(input.value);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    toast("Enter a port between 1 and 65535.");
    return;
  }
  window.open(withToken(workspacePortURL(workspace.name, port)), "_blank", "noopener");
}

function openDetailPort() {
  openPort(state.detailWorkspace, els.detailPortInput);
}

function openPortsDialogPort() {
  openPort(state.portsWorkspace, els.portsInput);
}

async function loadDetails(workspace, request) {
  try {
    const res = await api(`/api/workspaces/${encodeURIComponent(workspace.name)}`);
    if (request !== state.detailRequest) return;
    renderDetails(res);
  } catch (err) {
    if (request !== state.detailRequest) return;
    toast(err.message || "Failed to load details");
    renderDetails(workspace);
  }
}

async function loadLogs(workspace, request) {
  setLogsMessage("Loading startup logs...");
  try {
    const logs = await api(`/api/workspaces/${encodeURIComponent(workspace.name)}/logs`);
    if (request !== state.detailRequest) return;
    renderLogs(logs);
  } catch (err) {
    if (request !== state.detailRequest) return;
    setLogsMessage(err.message || "Failed to load startup logs.");
  }
}

function setLogsMessage(message) {
  els.detailLogsBody.replaceChildren();
  const note = document.createElement("p");
  note.className = "hint";
  note.textContent = message;
  els.detailLogsBody.append(note);
}

async function loadManifests(workspace, request) {
  if (!state.me || !state.me.isAdmin) return;
  setManifestsMessage("Loading manifests...");
  try {
    const data = await api(`/api/workspaces/${encodeURIComponent(workspace.name)}/manifests`);
    if (request !== state.detailRequest) return;
    renderManifests(data);
  } catch (err) {
    if (request !== state.detailRequest) return;
    setManifestsMessage(err.message || "Failed to load manifests.");
  }
}

function setManifestsMessage(message) {
  state.manifests = null;
  els.detailManifestsNote.textContent = message;
  els.detailManifestsBody.querySelector("code").textContent = message;
  syncManifestTabs();
}

function renderManifests(data) {
  state.manifests = data || null;
  const pod = data && data.pod ? data.pod : "";
  const pvc = data && data.pvc ? data.pvc : "";
  const pvcTab = document.querySelector('[data-manifest-tab="pvc"]');
  if (pvcTab) {
    pvcTab.disabled = !pvc;
    pvcTab.title = pvc ? "" : "No persistent volume claim for this workspace";
  }
  if (state.manifestTab === "pvc" && !pvc) state.manifestTab = "pod";

  const yaml = state.manifestTab === "pvc" ? pvc : pod;
  const fallback = state.manifestTab === "pvc"
    ? "No PersistentVolumeClaim for this workspace."
    : "No Pod manifest available.";
  els.detailManifestsBody.querySelector("code").textContent = yaml || fallback;

  const notes = [];
  if (data) {
    notes.push(data.podSource === "stopped-state"
      ? "Rendered from the saved stopped-workspace state."
      : "Live Pod from the cluster.");
    if (data.template) {
      notes.push(`Template ${data.template}${data.templateVersion ? ` @ ${data.templateVersion}` : ""}.`);
    }
    if (Array.isArray(data.redactedEnv) && data.redactedEnv.length) {
      notes.push(`Redacted environment values: ${data.redactedEnv.join(", ")}.`);
    } else {
      notes.push("Sensitive environment values are redacted.");
    }
  }
  els.detailManifestsNote.textContent = notes.join(" ") || "Rendered Pod and PVC definitions.";
  syncManifestTabs();
}

function syncManifestTabs() {
  for (const button of document.querySelectorAll("[data-manifest-tab]")) {
    const active = button.dataset.manifestTab === state.manifestTab;
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", active ? "true" : "false");
  }
}

async function copyManifests() {
  if (!state.manifests) {
    toast("No manifest to copy.");
    return;
  }
  const text = state.manifestTab === "pvc" ? state.manifests.pvc : state.manifests.pod;
  if (!text) {
    toast("No manifest to copy.");
    return;
  }
  try {
    await navigator.clipboard.writeText(text);
    toast("Manifest copied.", "success");
  } catch {
    const range = document.createRange();
    range.selectNodeContents(els.detailManifestsBody);
    const selection = window.getSelection();
    selection.removeAllRanges();
    selection.addRange(range);
    toast("Press Ctrl+C to copy the manifest.");
  }
}

function renderLogs(logs) {
  els.detailLogsBody.replaceChildren();
  if (!logs || !Array.isArray(logs.containers) || !logs.containers.length) {
    setLogsMessage((logs && logs.message) || "No startup logs yet.");
    return;
  }

  for (const container of logs.containers) {
    const block = document.createElement("details");
    block.className = "log-block";
    block.open = true;

    const summary = document.createElement("summary");
    const name = document.createElement("span");
    name.className = "log-name";
    name.textContent = container.name;
    summary.append(name);

    if (container.type === "init") {
      const badge = document.createElement("span");
      badge.className = "badge neutral";
      badge.textContent = "init";
      summary.append(badge);
    }

    const state = document.createElement("span");
    state.className = `log-state ${statusClass(container.state)}`;
    state.textContent = container.reason || container.state || "Unknown";
    if (container.reason) {
      state.title = container.message || container.reason;
    }
    summary.append(state);
    block.append(summary);

    const output = document.createElement("pre");
    output.className = "log-output";
    const code = document.createElement("code");
    if (container.error) {
      code.textContent = `Could not load logs: ${container.error}`;
    } else {
      code.textContent = container.logs || "(no output yet)";
    }
    output.append(code);
    block.append(output);
    els.detailLogsBody.append(block);
  }
}
function renderDetails(ws) {
  renderDetailHeader(ws);
  els.detailList.replaceChildren();

  const status = document.createElement("span");
  status.className = "status";
  const dot = document.createElement("span");
  dot.className = `dot ${statusClass(ws.phase)}`;
  status.append(dot, document.createTextNode(ws.phase || "Unknown"));

  els.detailList.append(detailRow("Status", status));
  els.detailList.append(detailRow("Auto-stop", scheduleSummary(ws.schedule) || "Disabled"));
  if (ws.region) {
    els.detailList.append(detailRow("Region", ws.regionDisplayName || ws.region));
  }
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
  if (Array.isArray(ws.volumes) && ws.volumes.length) {
    const volumesEl = document.createElement("div");
    volumesEl.className = "volumes";
    for (const v of ws.volumes) {
      const row = document.createElement("div");
      row.textContent = `${v.name || v.volumeName || "volume"} · ${v.capacity || "?"} · ${v.storageClass || "default"} · ${v.phase || ""}`;
      volumesEl.append(row);
    }
    els.detailList.append(detailRow("Volumes", volumesEl));
  }
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
  renderScheduleForm(ws.schedule);
}

function renderScheduleForm(schedule) {
  const enabled = Boolean(schedule && schedule.enabled);
  els.detailScheduleEnabled.checked = enabled;
  populateTimezones(els.detailScheduleTimezone, schedule && schedule.timezone);
  els.detailScheduleStart.value = schedule && schedule.start ? schedule.start : "09:00";
  els.detailScheduleStop.value = schedule && schedule.stop ? schedule.stop : "18:00";
  syncScheduleFields(els.detailScheduleForm, enabled);
}

async function saveSchedule(event) {
  event.preventDefault();
  const workspace = state.detailWorkspace;
  if (!workspace) return;
  const enabled = els.detailScheduleEnabled.checked;
  const body = {
    enabled,
    timezone: els.detailScheduleTimezone.value,
    start: els.detailScheduleStart.value,
    stop: els.detailScheduleStop.value,
  };
  if (enabled && (!body.timezone || !body.start || !body.stop)) {
    toast("Choose a time zone and start/stop times.");
    return;
  }
  const original = els.detailScheduleSave.innerHTML;
  els.detailScheduleSave.disabled = true;
  els.detailScheduleSave.setAttribute("aria-busy", "true");
  els.detailScheduleSave.innerHTML = `<span class="spinner small"></span>Saving`;
  try {
    const updated = await api(`/api/workspaces/${encodeURIComponent(workspace.name)}/schedule`, {
      method: "PUT",
      body: JSON.stringify(body),
    });
    state.detailWorkspace = updated;
    toast("Schedule saved.", "success");
    await refreshDetails();
    await load({ silent: true });
  } catch (err) {
    toast(err.message || "Failed to save schedule.");
  } finally {
    els.detailScheduleSave.disabled = false;
    els.detailScheduleSave.removeAttribute("aria-busy");
    els.detailScheduleSave.innerHTML = original;
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
  if (state.config.localMode) {
    els.sshNote.textContent += " The PyCharm button writes the local SSH config automatically.";
  } else if (state.config.ssh) {
    els.sshNote.textContent += " For PyCharm, enable \"Parse config file ~/.ssh/config\" in JetBrains Gateway.";
  }
  renderSSHIDEActions(workspace);
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
    if (!els.workspaceDetailView.hidden && state.detailWorkspace && state.detailWorkspace.name === workspace.name) {
      await refreshDetails();
    }
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
