"use strict";

let pendingAccessWorkspace = null;

function accessAllowed(workspace) {
  return !workspace || !workspace.access || workspace.access.allowed;
}

function ensureWorkspaceAccess(workspace) {
  if (accessAllowed(workspace)) return true;
  if (workspace.access && workspace.access.pending) {
    toast("Access request already sent. Waiting for the owner to approve.");
    return false;
  }
  promptWorkspaceAccess(workspace);
  return false;
}

function promptWorkspaceAccess(workspace) {
  if (!workspace) return;
  if (!els.accessDialog || !els.accessText) {
    toast("Owner approval is required to enter this workspace.");
    return;
  }
  pendingAccessWorkspace = workspace;
  const owner = workspace.access && workspace.access.owner ? workspace.access.owner : workspace.owner;
  els.accessText.textContent = owner
    ? `${owner} owns "${workspace.name}". They must approve before you can open a terminal, an IDE, a port or an SSH session.`
    : `The owner of "${workspace.name}" must approve before you can enter.`;
  if (!els.accessDialog.open) els.accessDialog.showModal();
}

async function requestAccessFromDialog() {
  const workspace = pendingAccessWorkspace;
  if (!workspace) return;
  els.accessRequest.disabled = true;
  try {
    const data = await api(`/api/workspaces/${encodeURIComponent(workspace.name)}/access/request`, {
      method: "POST",
      body: JSON.stringify({}),
    });
    toast(data && data.status === "approved" ? "Access granted." : "Access requested. Waiting for the owner.", "success");
    els.accessDialog.close();
    await load({ silent: true });
  } catch (err) {
    toast(err.message || "Could not request access.");
  } finally {
    els.accessRequest.disabled = false;
  }
}

async function updateWorkspaceAccess(workspace, action, actor) {
  try {
    const info = await api(`/api/workspaces/${encodeURIComponent(workspace.name)}/access/${action}`, {
      method: "POST",
      body: JSON.stringify({ actor }),
    });
    const verb = action === "approve" ? "approved" : action === "deny" ? "denied" : "revoked";
    toast(`Access ${verb} for ${actor}.`, "success");
    if (state.detailWorkspace && state.detailWorkspace.name === workspace.name) {
      state.detailWorkspace.access = info;
      renderAccessPanel(state.detailWorkspace);
    }
    await load({ silent: true });
    await loadAccessRequests({ silent: true });
  } catch (err) {
    toast(err.message || "Could not update access.");
  }
}

async function loadAccessRequests({ silent = false } = {}) {
  if (!els.accessBanner || state.accessRequestsLoading) return;
  state.accessRequestsLoading = true;
  try {
    const data = await api("/api/access/requests");
    state.accessRequests = Array.isArray(data) ? data : [];
    state.accessRequestsAt = Date.now();
    renderAccessRequests();
  } catch (err) {
    state.accessRequests = [];
    state.accessRequestsAt = Date.now();
    renderAccessRequests();
    if (!silent) toast(err.message || "Failed to load access requests.");
  } finally {
    state.accessRequestsLoading = false;
  }
}

function renderAccessRequests() {
  if (!els.accessBanner) return;
  const requests = state.accessRequests || [];
  const total = requests.reduce((sum, request) => sum + (Array.isArray(request.actors) ? request.actors.length : 0), 0);
  els.accessBanner.hidden = total === 0;
  if (!total) {
    els.accessBannerList.replaceChildren();
    return;
  }
  const workspaceCount = requests.length;
  els.accessBannerTitle.textContent = total === 1 ? "Admin access needs your approval" : `${total} admin access requests need your approval`;
  els.accessBannerText.textContent = total === 1
    ? `An administrator is asking to connect to "${requests[0].workspace}". Approve only if you trust this request.`
    : `Administrators are asking to connect to ${workspaceCount === 1 ? `"${requests[0].workspace}"` : `${workspaceCount} of your workspaces`}. Approve only if you trust these requests.`;
  els.accessBannerList.replaceChildren();

  for (const request of requests) {
    for (const actor of request.actors || []) {
      const row = document.createElement("div");
      row.className = "access-banner-row";
      const text = document.createElement("span");
      text.className = "access-banner-request";
      const actorNode = document.createElement("strong");
      actorNode.textContent = actor;
      const workspaceNode = document.createElement("span");
      workspaceNode.textContent = request.workspace;
      text.append(actorNode, document.createTextNode(" wants to connect to "), workspaceNode);
      if (request.region) {
        const region = document.createElement("span");
        region.className = "badge neutral";
        region.textContent = request.region;
        text.append(" ", region);
      }
      const approve = document.createElement("button");
      approve.type = "button";
      approve.className = "button small start";
      approve.textContent = "Approve";
      const deny = document.createElement("button");
      deny.type = "button";
      deny.className = "button small danger";
      deny.textContent = "Deny";
      const run = async (action) => {
        approve.disabled = true;
        deny.disabled = true;
        await updateWorkspaceAccess({ name: request.workspace }, action, actor);
        approve.disabled = false;
        deny.disabled = false;
      };
      approve.addEventListener("click", () => run("approve"));
      deny.addEventListener("click", () => run("deny"));
      row.append(text, approve, deny);
      els.accessBannerList.append(row);
    }
  }
}

function renderAccessPanel(workspace) {
  if (!els.detailAccess) return;
  els.detailAccess.replaceChildren();
  const access = workspace && workspace.access;
  if (!access) return;

  const presence = Array.isArray(access.presence) ? access.presence : [];
  const presenceBox = document.createElement("div");
  presenceBox.className = "access-presence";
  const presenceTitle = document.createElement("h4");
  presenceTitle.textContent = "Connected";
  presenceBox.append(presenceTitle);
  if (!presence.length) {
    const empty = document.createElement("p");
    empty.className = "hint";
    empty.textContent = "Nobody is connected right now.";
    presenceBox.append(empty);
  } else {
    for (const item of presence) {
      const line = document.createElement("div");
      line.className = "access-person";
      const name = document.createElement("span");
      name.textContent = item.actor;
      const kinds = document.createElement("span");
      kinds.className = "badge neutral";
      kinds.textContent = item.kinds && item.kinds.length ? item.kinds.join(", ") : "session";
      line.append(name, kinds);
      presenceBox.append(line);
    }
  }
  els.detailAccess.append(presenceBox);

  if (access.isOwner) {
    const requests = Array.isArray(access.pendingRequests) ? access.pendingRequests : [];
    const box = document.createElement("div");
    box.className = "access-requests";
    const title = document.createElement("h4");
    title.textContent = "Access requests";
    box.append(title);
    if (!requests.length) {
      const empty = document.createElement("p");
      empty.className = "hint";
      empty.textContent = "No pending request.";
      box.append(empty);
    } else {
      for (const actor of requests) {
        const row = document.createElement("div");
        row.className = "access-person";
        const name = document.createElement("span");
        name.textContent = actor;
        const approve = document.createElement("button");
        approve.type = "button";
        approve.className = "button small start";
        approve.textContent = "Approve";
        approve.addEventListener("click", () => updateWorkspaceAccess(workspace, "approve", actor));
        const deny = document.createElement("button");
        deny.type = "button";
        deny.className = "button small danger";
        deny.textContent = "Deny";
        deny.addEventListener("click", () => updateWorkspaceAccess(workspace, "deny", actor));
        row.append(name, approve, deny);
        box.append(row);
      }
    }
    els.detailAccess.append(box);

    const grants = Array.isArray(access.grants) ? access.grants : [];
    const grantsBox = document.createElement("div");
    grantsBox.className = "access-requests";
    const grantsTitle = document.createElement("h4");
    grantsTitle.textContent = "Active grants";
    grantsBox.append(grantsTitle);
    if (!grants.length) {
      const empty = document.createElement("p");
      empty.className = "hint";
      empty.textContent = "No active grant.";
      grantsBox.append(empty);
    } else {
      for (const grant of grants) {
        const row = document.createElement("div");
        row.className = "access-person";
        const name = document.createElement("span");
        name.textContent = grant.actor;
        const expires = document.createElement("span");
        expires.className = "hint";
        expires.textContent = grant.expiresAt ? `until ${new Date(grant.expiresAt).toLocaleString()}` : "";
        const revoke = document.createElement("button");
        revoke.type = "button";
        revoke.className = "button small danger";
        revoke.textContent = "Revoke";
        revoke.addEventListener("click", () => updateWorkspaceAccess(workspace, "revoke", grant.actor));
        row.append(name, expires, revoke);
        grantsBox.append(row);
      }
    }
    els.detailAccess.append(grantsBox);
  } else if (access.isAdmin) {
    const box = document.createElement("div");
    box.className = "access-requests";
    const title = document.createElement("h4");
    title.textContent = "Your access";
    box.append(title);
    const line = document.createElement("div");
    line.className = "access-person";
    if (access.allowed) {
      const text = document.createElement("span");
      text.textContent = access.grantExpiresAt
        ? `Granted until ${new Date(access.grantExpiresAt).toLocaleString()}`
        : "Allowed";
      line.append(text);
    } else {
      const text = document.createElement("span");
      text.textContent = access.pending ? "Waiting for owner approval" : "Owner approval required";
      const button = document.createElement("button");
      button.type = "button";
      button.className = "button small primary";
      button.textContent = access.pending ? "Requested" : "Request access";
      button.disabled = Boolean(access.pending);
      button.addEventListener("click", () => promptWorkspaceAccess(workspace));
      line.append(text, button);
    }
    box.append(line);
    els.detailAccess.append(box);
  }
}

if (els.accessRequest) els.accessRequest.addEventListener("click", requestAccessFromDialog);
if (els.accessDialog) els.accessDialog.addEventListener("close", () => {
  pendingAccessWorkspace = null;
});

const workspaceMarks = {
  vscode: { icon: "vscode", tone: "vscode" },
  cursor: { icon: "cursor", tone: "cursor" },
  pycharm: { icon: "pycharm", tone: "pycharm" },
  clion: { icon: "clion", tone: "clion" },
  jupyterlab: { icon: "jupyterlab", tone: "jupyterlab" },
  "vscode-web": { icon: "code", tone: "code" },
};

function workspaceMark(workspace, extraClass = "") {
  const apps = Array.isArray(workspace.apps) ? workspace.apps : [];
  const app = apps.find((id) => workspaceMarks[id]);
  const mark = workspaceMarks[app] || { icon: "box", tone: "default" };
  const node = document.createElement("span");
  node.className = `workspace-mark tone-${mark.tone}${extraClass ? ` ${extraClass}` : ""}`;
  node.innerHTML = icon(mark.icon, mark.icon === "vscode" ? "icon brand-icon" : "icon");
  return node;
}

function statusPill(phase) {
  const status = document.createElement("span");
  const state = statusClass(phase) || "unknown";
  status.className = `status status-${state}`;
  const dot = document.createElement("span");
  dot.className = `dot ${state}`;
  status.append(dot, document.createTextNode(phase || "Unknown"));
  return status;
}

const WORKSPACE_WASTE_SIGNALS = new Set([
  "idle-cpu",
  "idle-memory",
  "oversized",
  "contended",
  "missing-requests",
  "restarts",
  "oom",
]);
const WORKSPACE_WASTE_DANGER = new Set(["oversized", "contended", "oom"]);

function workspaceWasteInfo(workspace) {
  const item = state.workspaceInsights && state.workspaceInsights[workspace.name];
  if (!item || !Array.isArray(item.signals)) return null;
  const signals = item.signals.filter((signal) => WORKSPACE_WASTE_SIGNALS.has(signal));
  if (!signals.length) return null;

  const recommendations = [];
  const add = (text) => {
    if (text && !recommendations.includes(text)) recommendations.push(text);
  };
  const usageCPU = item.usage ? Number(item.usage.cpuCores) : NaN;
  const requestCPU = item.requests ? Number(item.requests.cpuCores) : NaN;
  const usageMemory = item.usage ? Number(item.usage.memoryBytes) : NaN;
  const requestMemory = item.requests ? Number(item.requests.memoryBytes) : NaN;

  if (signals.includes("idle-cpu")) {
    if (Number.isFinite(usageCPU) && Number.isFinite(requestCPU) && requestCPU > 0) {
      const target = Math.max(0.05, Math.round(usageCPU * 15) / 10);
      add(`Lower the CPU request from ${formatCPU(requestCPU)} to about ${formatCPU(target)}.`);
    } else {
      add("Lower the CPU request to match observed usage.");
    }
  }
  if (signals.includes("idle-memory")) {
    if (Number.isFinite(usageMemory) && Number.isFinite(requestMemory) && requestMemory > 0) {
      const target = Math.max(64 * 1024 * 1024, Math.ceil((usageMemory * 1.25) / (1024 * 1024)) * 1024 * 1024);
      add(`Lower the memory request from ${formatBytes(requestMemory)} to about ${formatBytes(target)}.`);
    } else {
      add("Lower the memory request to match observed usage.");
    }
  }
  if (signals.includes("oversized")) add("Requests are far above real usage: right-size CPU and memory.");
  if (signals.includes("contended")) add("CPU usage is above the request: raise the CPU request or cap the workload.");
  if (signals.includes("missing-requests")) add("Set CPU and memory requests so waste can be measured and scheduled.");
  if (signals.includes("restarts")) add("Restarts detected: check crash loops before resizing.");
  if (signals.includes("oom")) add("OOM kills detected: raise the memory limit before adjusting requests.");
  if (!recommendations.length) add("Review this workspace's CPU and memory requests.");
  if (!workspace.schedule || !workspace.schedule.enabled) {
    add("Allow automatic shutdown outside working hours to release idle capacity when you are away.");
  }

  const tone = signals.some((signal) => WORKSPACE_WASTE_DANGER.has(signal)) ? "danger" : "warning";
  return {
    tone,
    label: tone === "danger" ? "Waste alert" : "Waste warning",
    signals,
    recommendations,
    bestPractice: "Save more: right-size requests, schedule shutdown outside working hours, and remove unused volumes.",
  };
}

function workspaceWasteBadge(info) {
  const badge = document.createElement("span");
  badge.className = `badge waste tone-${info.tone}`;
  badge.innerHTML = `${icon("alert")}<span></span>`;
  badge.querySelector("span").textContent = info.label;
  badge.title = info.recommendations.join("\n");
  return badge;
}

function renderCard(workspace) {
  const card = document.createElement("article");
  card.className = `card card-${statusClass(workspace.phase) || "unknown"}`;

  const top = document.createElement("div");
  top.className = "card-top";

  const title = document.createElement("div");
  title.className = "card-title";
  const mark = workspaceMark(workspace);
  const name = document.createElement("h2");
  const nameButton = document.createElement("button");
  nameButton.type = "button";
  nameButton.className = "workspace-name-button";
  nameButton.textContent = workspace.name;
  nameButton.title = workspace.name;
  nameButton.addEventListener("click", () => showDetails(workspace));
  name.append(nameButton);
  title.append(mark, name);

  const status = statusPill(workspace.phase);
  const waste = workspaceWasteInfo(workspace);

  top.append(title);

  const meta = document.createElement("div");
  meta.className = "card-meta";

  const metaRow = document.createElement("div");
  metaRow.className = "card-meta-row";
  metaRow.append(status);

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

  const access = workspace.access;
  if (access && Array.isArray(access.presence) && access.presence.length) {
    const presence = document.createElement("span");
    presence.className = "badge presence";
    presence.innerHTML = `${icon("users")}<span></span>`;
    presence.querySelector("span").textContent = access.presence
      .map((item) => `${item.actor}${item.kinds && item.kinds.length ? ` (${item.kinds.join(", ")})` : ""}`)
      .join(", ");
    presence.title = "Connected now";
    metaRow.append(presence);
  }

  if (access && access.isOwner && Array.isArray(access.pendingRequests) && access.pendingRequests.length) {
    const pending = document.createElement("span");
    pending.className = "badge access-requested";
    pending.innerHTML = `${icon("alert")}<span>Access requested</span>`;
    pending.title = access.pendingRequests.join(", ");
    metaRow.append(pending);
  }

  if (waste) metaRow.append(workspaceWasteBadge(waste));

  meta.append(metaRow);
  if (workspace.createdAt) {
    const created = document.createElement("span");
    created.className = "card-created";
    created.innerHTML = `${icon("clock")}<span></span>`;
    created.querySelector("span").textContent = `Created ${formatAge(workspace.createdAt)}`;
    meta.append(created);
  }
  if (waste) {
    const note = document.createElement("p");
    note.className = `card-waste-note tone-${waste.tone}`;
    note.innerHTML = `${icon("alert")}<span></span>`;
    note.querySelector("span").textContent = waste.recommendations[0];
    note.title = waste.recommendations.join("\n");
    const tip = document.createElement("p");
    tip.className = "card-waste-tip";
    tip.textContent = waste.bestPractice;
    tip.title = waste.recommendations.join("\n");
    meta.append(note, tip);
  }

  const actions = renderWorkspaceActions(workspace);
  actions.classList.add("card-actions");

  card.append(top, meta, actions);
  card.classList.add("card-clickable");
  card.addEventListener("click", (event) => {
    if (event.target.closest("button, a, input, select, textarea")) return;
    showDetails(workspace);
  });
  return card;
}

function workspaceOpenGroups(workspace, running) {
  const ides = workspaceIDEs(workspace).map((ide) => ({
    ide,
    label: ide.label,
    icon: ide.icon,
    brand: ide.brand,
    title: ide.title(ideUsesLocalLaunch(ide)),
    disabled: !running,
    onSelect: () => launchWorkspaceIDE(ide, workspace),
  }));

  const connect = [];
  if (state.config.ssh) {
    connect.push({
      label: "SSH",
      icon: "key",
      title: "Open an SSH session",
      onSelect: () => {
        if (ensureWorkspaceAccess(workspace)) showSSH(workspace);
      },
    });
  }
  connect.push({
    label: "Ports",
    icon: "server",
    title: "Open an application running in this workspace",
    onSelect: () => {
      if (ensureWorkspaceAccess(workspace)) showPorts(workspace);
    },
  });
  connect.push({
    label: "Terminal",
    icon: "terminal",
    title: running ? "Open a web terminal" : "Workspace is not running",
    disabled: !running,
    onSelect: () => {
      if (ensureWorkspaceAccess(workspace)) window.location.href = withToken(`/terminal?name=${encodeURIComponent(workspace.name)}`);
    },
  });

  const links = [];
  const template = templateByName(workspace.template);
  for (const link of (template && Array.isArray(template.links) ? template.links : [])) {
    const href = resolveLink(link, workspace);
    if (!href) continue;
    const iconName = ["vscode", "cursor", "pycharm", "clion", "jetbrains", "jupyterlab", "terminal", "code", "link", "external"].includes(link.icon) ? link.icon : "external";
    links.push({
      label: link.name,
      icon: iconName,
      brand: ["vscode", "cursor", "pycharm", "clion", "jetbrains", "jupyterlab"].includes(iconName),
      href,
      target: link.newTab ? "_blank" : "",
      title: href,
    });
  }

  return { ides, connect, links };
}

function workspaceOpenMenu(groups) {
  const items = [];
  if (groups.ides.length) {
    items.push({ heading: "Editors" }, ...groups.ides);
  }
  if (groups.connect.length) {
    items.push({ heading: "Connect" }, ...groups.connect);
  }
  if (groups.links.length) {
    items.push({ heading: "Links" }, ...groups.links);
  }
  return items;
}

function workspaceOpenControl(workspace, running) {
  const groups = workspaceOpenGroups(workspace, running);
  const apps = Array.isArray(workspace.apps) ? workspace.apps : [];
  const primaryEntry = groups.ides.find((item) => apps.includes(item.ide.id))
    || groups.ides[0]
    || groups.connect.find((item) => item.label === "Terminal");
  if (!primaryEntry) return null;

  const wrap = document.createElement("div");
  wrap.className = "split-action";

  const primary = document.createElement("button");
  primary.type = "button";
  primary.className = "button small split-primary";
  primary.innerHTML = `${icon(primaryEntry.icon, primaryEntry.brand ? "icon brand-icon" : "icon")}<span></span>`;
  primary.querySelector("span").textContent = primaryEntry.label;
  primary.title = primaryEntry.title || `Open ${primaryEntry.label}`;
  if (primaryEntry.disabled) {
    primary.disabled = true;
  } else {
    primary.addEventListener("click", () => {
      if (primaryEntry.ide) launchWorkspaceIDE(primaryEntry.ide, workspace, primary);
      else if (primaryEntry.onSelect) primaryEntry.onSelect();
    });
  }

  const toggle = document.createElement("button");
  toggle.type = "button";
  toggle.className = "button small split-toggle";
  toggle.title = "More ways to open";
  toggle.setAttribute("aria-label", "More ways to open");
  toggle.setAttribute("aria-haspopup", "menu");
  toggle.setAttribute("aria-expanded", "false");
  toggle.innerHTML = icon("chevron-down");
  const panel = actionMenuPanel(workspaceOpenMenu(groups));
  toggle.addEventListener("click", (event) => toggleActionMenu(toggle, panel, event));

  wrap.append(primary, toggle);
  return wrap;
}

function renderWorkspaceActions(workspace) {
  const actions = document.createElement("div");
  actions.className = "workspace-action-bar";
  const stopped = workspace.phase === "Stopped";
  const running = workspace.phase === "Running";

  if (stopped) {
    const start = document.createElement("button");
    start.type = "button";
    start.className = "button small start";
    start.innerHTML = `${icon("play")}Start`;
    start.addEventListener("click", () => runWorkspaceAction(workspace, "start", start));
    actions.append(start);
  } else {
    const open = workspaceOpenControl(workspace, running);
    if (open) actions.append(open);
  }

  const overflow = [
    { label: "Details", icon: "info", onSelect: () => showDetails(workspace) },
  ];
  if (!stopped) {
    overflow.push(
      { label: "Restart", icon: "refresh", onSelect: () => confirmWorkspaceAction(workspace, "restart") },
      { label: "Stop", icon: "stop", onSelect: () => confirmWorkspaceAction(workspace, "stop") },
    );
  }
  overflow.push(
    { separator: true },
    { label: "Delete workspace", icon: "trash", danger: true, onSelect: () => confirmDelete(workspace) },
  );
  actions.append(actionMenuOverflow(overflow));

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
  const nameMark = workspaceMark(workspace, "small");
  const nameButton = document.createElement("button");
  nameButton.type = "button";
  nameButton.className = "workspace-name-button";
  nameButton.textContent = workspace.name;
  nameButton.title = workspace.name;
  nameButton.addEventListener("click", () => showDetails(workspace));
  nameWrap.append(nameMark, nameButton);
  const waste = workspaceWasteInfo(workspace);
  if (waste) nameWrap.append(workspaceWasteBadge(waste));
  nameCell.append(nameWrap);

  const statusCell = document.createElement("td");
  statusCell.append(statusPill(workspace.phase));

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
  actionsCell.append(actions);

  row.append(nameCell, statusCell, templateCell, ownerCell, createdCell, actionsCell);
  return row;
}
const workspaceInsightState = {
  request: 0,
  loadedAt: 0,
  pending: false,
};

async function loadWorkspaceInsights({ force = false } = {}) {
  if (typeof monitoringLiveEnabled !== "function" || !monitoringLiveEnabled()) return;
  if (workspaceInsightState.pending) return;
  const now = Date.now();
  if (!force && workspaceInsightState.loadedAt && now - workspaceInsightState.loadedAt < 60000) return;
  workspaceInsightState.pending = true;
  const request = ++workspaceInsightState.request;
  try {
    const report = await api("/api/monitoring/finops");
    if (request !== workspaceInsightState.request) return;
    const insights = {};
    for (const item of (Array.isArray(report.workspaces) ? report.workspaces : [])) {
      if (item && item.name) insights[item.name] = item;
    }
    state.workspaceInsights = insights;
    workspaceInsightState.loadedAt = Date.now();
    renderWorkspaces();
    if (state.detailWorkspace) {
      const fresh = state.workspaces.find((item) => item.name === state.detailWorkspace.name);
      renderDetailHeader(fresh || state.detailWorkspace);
    }
  } catch {
    workspaceInsightState.loadedAt = Date.now();
  } finally {
    workspaceInsightState.pending = false;
  }
}

async function load({ silent = false } = {}) {
  if (state.loading) {
    state.reloadPending = true;
    return;
  }
  state.loading = true;
  if (!silent && !state.loaded) els.loading.hidden = false;
  loadWorkspaceInsights();

  try {
    const headers = {};
    if (state.workspacesEtag) headers["If-None-Match"] = state.workspacesEtag;
    const result = await api(
      pageURL("/api/workspaces", state.workspacePage, state.workspacePerPage, state.workspaceQuery.trim()),
      { headers, withMeta: true },
    );
    if (result === apiNotModified) {
      state.loaded = true;
      state.pollError = false;
      schedulePoll(30000);
      return;
    }
    const data = result.data;
    if (result.etag) state.workspacesEtag = result.etag;
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
    if (state.detailWorkspace && els.workspaceDetailView && !els.workspaceDetailView.hidden) {
      const fresh = state.workspaces.find((item) => item.name === state.detailWorkspace.name);
      if (fresh) {
        state.detailWorkspace = { ...state.detailWorkspace, access: fresh.access, owner: fresh.owner };
        renderAccessPanel(state.detailWorkspace);
        renderDetailHeader(state.detailWorkspace);
      }
    }
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
    if (Date.now() - state.accessRequestsAt > 30000) loadAccessRequests({ silent: true });
    schedulePoll(5000);
  } catch (err) {
    if (!silent || !state.pollError) {
      toast(err.message || "Failed to load workspaces.");
      state.pollError = true;
    }
    schedulePoll(30000);
  } finally {
    state.loading = false;
    els.loading.hidden = true;
    if (state.reloadPending) {
      state.reloadPending = false;
      load({ silent: true });
    }
  }
}
function workspaceNamePrefix() {
  if (!state.me || state.me.method === "none") return "";
  let raw = state.me.username || state.me.email || "";
  if (raw.includes("@")) raw = raw.split("@")[0];
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
  setScheduleDays(els.createForm, []);
  syncScheduleFields(els.createForm, false);
  await loadExternalAuth();
  renderParams();
  els.createDialog.showModal();
  requestAnimationFrame(() => els.nameInput.focus());
  loadCreateQuota();
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
    case "sshHost":
      return state.config.mesh ? sshAlias(workspace.name, workspace.region) : (state.config.sshHost || "127.0.0.1");
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
  state.detailWorkspace = workspace;
  state.manifests = null;
  state.manifestTab = "pod";
  renderDetailHeader(workspace);
  els.detailList.replaceChildren();
  els.detailEnvironmentBody.replaceChildren();
  setDetailEnvironmentMessage("Loading environment...");
  els.detailQuotaBody.replaceChildren();
  setDetailQuotaMessage("Loading quota...");
  els.detailLogsBody.replaceChildren();
  els.detailManifests.hidden = !(state.me && state.me.isAdmin);
  if (!els.detailManifests.hidden) setManifestsMessage("Loading manifests...");
  els.detailPortsBody.replaceChildren();
  els.detailPortInput.value = "";
  if (typeof resetWorkspaceMonitoring === "function") resetWorkspaceMonitoring();

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
  if (typeof stopWorkspaceMonitoring === "function") stopWorkspaceMonitoring();
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
    : "Ports, metadata, environment, quota, rendered manifests, and startup logs for this workspace.";

  const phase = workspace.phase || "Unknown";
  els.detailStatus.replaceChildren();
  const badge = document.createElement("span");
  badge.className = `detail-status-badge ${statusClass(phase)}`;
  badge.innerHTML = `<span class="dot ${statusClass(phase)}"></span><span></span>`;
  badge.querySelector("span:last-child").textContent = phase;
  els.detailStatus.append(badge);
  const waste = workspaceWasteInfo(workspace);
  if (waste) els.detailStatus.append(workspaceWasteBadge(waste));

  const actions = renderWorkspaceActions(workspace);
  actions.classList.add("detail-action-list");
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
  if (typeof loadWorkspaceMonitoring === "function") jobs.push(loadWorkspaceMonitoring(workspace, { silent: true }));
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
  if (!ensureWorkspaceAccess(workspace)) return;
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
  if (!ensureWorkspaceAccess(workspace)) return;
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
  renderDetailEnvironment(ws.environment);
  renderDetailQuota(ws.quota);
  renderAccessPanel(ws);
  renderScheduleForm(ws.schedule);
}

function setDetailEnvironmentMessage(message) {
  els.detailEnvironmentBody.replaceChildren();
  const note = document.createElement("p");
  note.className = "hint";
  note.textContent = message;
  els.detailEnvironmentBody.append(note);
}

function environmentSourceLabel(source) {
  const target = source && source.target ? source.target : "";
  switch (source && source.scope) {
    case "template":
      return target ? `Template · ${target}` : "Template";
    case "team":
      return target ? `Team · ${target}` : "Team";
    case "user":
      return target ? `User · ${target}` : "User";
    case "workspace":
      return target ? `Workspace · ${target}` : "Workspace";
    default:
      return "Unknown";
  }
}

function renderDetailEnvironment(items) {
  if (!Array.isArray(items)) {
    setDetailEnvironmentMessage("Environment information is not available.");
    return;
  }
  if (!items.length) {
    setDetailEnvironmentMessage("No managed environment variables for this workspace.");
    return;
  }
  const groups = new Map();
  for (const item of items) {
    const source = item.source || {};
    const key = `${source.scope || "unknown"}\u0000${source.target || ""}`;
    if (!groups.has(key)) {
      groups.set(key, { scope: source.scope || "unknown", target: source.target || "", items: [] });
    }
    groups.get(key).items.push(item);
  }
  const order = { workspace: 0, user: 1, team: 2, template: 3 };
  const sorted = [...groups.values()].sort((a, b) => {
    const left = order[a.scope] ?? 9;
    const right = order[b.scope] ?? 9;
    if (left !== right) return left - right;
    return a.target.localeCompare(b.target);
  });
  const list = document.createElement("div");
  list.className = "env-tree";
  for (const group of sorted) {
    const section = document.createElement("details");
    section.className = "env-source-group";
    section.open = true;

    const summary = document.createElement("summary");
    summary.className = "env-source-summary";
    const source = document.createElement("span");
    source.className = `env-source ${group.scope}`;
    source.textContent = environmentSourceLabel(group);
    const note = document.createElement("span");
    note.className = "env-source-note";
    note.textContent = environmentSourceNote(group.scope);
    const count = document.createElement("span");
    count.className = "env-source-count";
    count.textContent = `${group.items.length} variable${group.items.length === 1 ? "" : "s"}`;
    summary.append(source, note, count);

    const rows = document.createElement("div");
    rows.className = "env-detail-rows";
    for (const item of group.items) {
      const row = document.createElement("div");
      row.className = "env-detail-row";

      const name = document.createElement("code");
      name.className = "env-name";
      name.textContent = item.name || "";

      const value = document.createElement("span");
      value.className = item.secret ? "env-value secret" : "env-value";
      value.textContent = item.secret ? "Secret value hidden" : (item.value || "(empty)");

      row.append(name, value);
      rows.append(row);
    }
    section.append(summary, rows);
    list.append(section);
  }
  els.detailEnvironmentBody.replaceChildren(list);
}

function environmentSourceNote(scope) {
  switch (scope) {
    case "workspace":
      return "Highest precedence";
    case "user":
      return "Overrides teams and template";
    case "team":
      return "Overrides template";
    case "template":
      return "Template defaults";
    default:
      return "";
  }
}

function setDetailQuotaMessage(message) {
  els.detailQuotaBody.replaceChildren();
  const note = document.createElement("p");
  note.className = "hint";
  note.textContent = message;
  els.detailQuotaBody.append(note);
}

function detailQuotaSourceLabel(source, hasLimit) {
  if (!source || !source.scope || source.scope === "none") return hasLimit ? "Source unavailable" : "No limit";
  const target = source.target ? ` · ${source.target}` : "";
  if (source.scope === "user") return `User override${target}`;
  if (source.scope === "team") return `Team${target}`;
  return `${source.scope}${target}`;
}

function renderDetailQuota(quota) {
  if (!quota || !quota.effective) {
    setDetailQuotaMessage("Quota information is not available.");
    return;
  }
  const effective = quota.effective || {};
  const usage = quota.usage || {};
  const workspace = quota.workspace || {};
  const sources = quota.sources || {};
  const body = document.createElement("div");
  body.className = "workspace-quota-content";

  const note = document.createElement("p");
  note.className = "hint workspace-quota-note";
  const owner = quota.owner || "this owner";
  note.textContent = `Usage covers every workspace owned by ${owner}. This workspace requests ${workspace.cpu || "no CPU"} CPU, ${workspace.memory || "no memory"} memory, and ${workspace.storage || "no storage"} storage.`;

  const grid = document.createElement("div");
  grid.className = "quota-meter-grid workspace-quota-grid";
  const metrics = [
    ["Workspaces", usage.workspaces ?? 0, effective.maxWorkspaces, "workspaces", sources.maxWorkspaces],
    ["CPU", usage.cpu || "0", effective.cpu, "cpu", sources.cpu],
    ["Memory", usage.memory || "0", effective.memory, "memory", sources.memory],
    ["Storage", usage.storage || "0", effective.storage, "storage", sources.storage],
  ];
  for (const [label, used, limit, kind, source] of metrics) {
    const card = quotaMeterCard(label, used, limit, kind);
    const sourceNode = document.createElement("div");
    sourceNode.className = "quota-source";
    sourceNode.textContent = detailQuotaSourceLabel(source, limit !== undefined && limit !== null && String(limit).trim() !== "");
    card.append(sourceNode);
    grid.append(card);
  }
  body.append(note, grid);
  els.detailQuotaBody.replaceChildren(body);
}

function renderScheduleForm(schedule) {
  const enabled = Boolean(schedule && schedule.enabled);
  els.detailScheduleEnabled.checked = enabled;
  populateTimezones(els.detailScheduleTimezone, schedule && schedule.timezone);
  els.detailScheduleStart.value = schedule && schedule.start ? schedule.start : "09:00";
  els.detailScheduleStop.value = schedule && schedule.stop ? schedule.stop : "18:00";
  setScheduleDays(els.detailScheduleForm, schedule && schedule.days);
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
    days: scheduleDaysFromForm(els.detailScheduleForm),
    start: els.detailScheduleStart.value,
    stop: els.detailScheduleStop.value,
  };
  if (enabled && (!body.timezone || !body.start || !body.stop)) {
    toast("Choose a time zone and start/stop times.");
    return;
  }
  if (enabled && !body.days.length) {
    toast("Select at least one working day.");
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
  if (state.config.mesh) {
    const login = state.config.meshLoginURL || "your Headscale server";
    els.sshNote.textContent = `Direct WireGuard SSH. Join the tailnet once: tailscale up --login-server=${login}. Then use the config below.`;
  } else if (state.config.windows) {
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
async function confirmWorkspaceAction(workspace, action) {
  const stop = action === "stop";
  const message = stop
    ? workspace.persistent
      ? `Stop "${workspace.name}"? The pod is deleted and its volume is kept.`
      : `Stop "${workspace.name}"? This workspace has no persistent volume; its data will be lost.`
    : `Restart "${workspace.name}"? The pod is recreated and its volume is kept.`;
  const confirmed = await confirmAction({
    title: stop ? "Stop workspace" : "Restart workspace",
    message,
    confirmLabel: stop ? "Stop workspace" : "Restart workspace",
    danger: stop,
  });
  if (confirmed) runWorkspaceAction(workspace, action);
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
async function confirmDelete(workspace) {
  const confirmed = await confirmAction({
    title: "Delete workspace",
    message: `Delete "${workspace.name}"? This removes the pod and its persistent volume. This cannot be undone.`,
    confirmLabel: "Delete workspace",
  });
  if (!confirmed) return;
  try {
    await api(`/api/workspaces/${encodeURIComponent(workspace.name)}`, { method: "DELETE" });
    toast(`Workspace ${workspace.name} deleted.`, "success");
    if (state.detailWorkspace && state.detailWorkspace.name === workspace.name) {
      closeWorkspaceDetail();
    } else {
      await load();
    }
    await loadMyQuota();
    await loadAccessRequests({ silent: true });
  } catch (err) {
    toast(err.message || "Failed to delete workspace.");
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
