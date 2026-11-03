"use strict";

async function loadExternalAuth(ids = "") {
  try {
    const query = ids ? `?ids=${encodeURIComponent(ids)}` : "";
    const data = await api(`/api/external-auth${query}`);
    state.externalAuth = Array.isArray(data.providers) ? data.providers : [];
    state.externalAuthEnabled = Boolean(data.enabled);
    els.manageConnections.hidden = !state.externalAuthEnabled;
    return state.externalAuth;
  } catch {
    state.externalAuth = [];
    state.externalAuthEnabled = false;
    els.manageConnections.hidden = true;
    return [];
  }
}
function externalAuthStatus(id) {
  return state.externalAuth.find((provider) => provider.id === id);
}
function connectExternalAuth(id) {
  const status = externalAuthStatus(id);
  if (!status || !status.loginUrl) return;
  location.href = `${status.loginUrl}?next=${encodeURIComponent("/")}`;
}
async function disconnectExternalAuth(id) {
  const status = externalAuthStatus(id);
  if (!status) return;
  if (!window.confirm(`Disconnect ${status.displayName || id}? Workspaces will need to reconnect before they can use it.`)) return;
  try {
    await api(`/api/external-auth/${encodeURIComponent(id)}/disconnect`, { method: "POST" });
    await loadExternalAuth();
    renderConnections();
    renderExternalAuthNotice();
    toast(`${status.displayName || id} disconnected.`, "success");
  } catch (err) {
    toast(err.message || "Failed to disconnect external auth.");
  }
}
function connectionCard(status) {
  const card = document.createElement("article");
  card.className = "connection-card";
  const info = document.createElement("div");
  const title = document.createElement("h2");
  title.textContent = status.displayName || status.id;
  const meta = document.createElement("p");
  meta.className = "connection-meta";
  if (status.authenticated) {
    meta.textContent = status.expiresAt
      ? `Connected · access token expires ${new Date(status.expiresAt).toLocaleString()}`
      : "Connected";
  } else {
    meta.textContent = "Not connected";
  }
  info.append(title, meta);
  const actions = document.createElement("div");
  actions.className = "connection-actions";
  if (status.authenticated) {
    const disconnect = document.createElement("button");
    disconnect.type = "button";
    disconnect.className = "button danger small";
    disconnect.textContent = "Disconnect";
    disconnect.addEventListener("click", () => disconnectExternalAuth(status.id));
    actions.append(disconnect);
  } else {
    const connect = document.createElement("button");
    connect.type = "button";
    connect.className = "button primary small";
    connect.textContent = "Connect";
    connect.addEventListener("click", () => connectExternalAuth(status.id));
    actions.append(connect);
  }
  card.append(info, actions);
  return card;
}
function renderConnections() {
  els.connectionsList.replaceChildren();
  const providers = state.externalAuth;
  els.connectionsCount.textContent = `${providers.length} provider${providers.length === 1 ? "" : "s"}`;
  if (!providers.length) {
    els.connectionsList.append(emptyInline("No external auth providers are configured."));
    return;
  }
  for (const status of providers) {
    els.connectionsList.append(connectionCard(status));
  }
}
async function openConnections() {
  await loadExternalAuth();
  renderConnections();
  showView("connections");
}
function renderExternalAuthNotice() {
  if (!els.externalAuthNotice) return;
  const template = templateByName(els.templateSelect.value);
  const requirements = template && Array.isArray(template.externalAuth) ? template.externalAuth : [];
  els.externalAuthNotice.replaceChildren();
  if (!requirements.length || !state.externalAuthEnabled) {
    els.externalAuthNotice.hidden = true;
    if (!els.createSubmit.dataset.busy) els.createSubmit.disabled = false;
    return;
  }

  const heading = document.createElement("h3");
  heading.textContent = "External accounts";
  const hint = document.createElement("p");
  hint.className = "hint";
  hint.textContent = "This template uses these accounts inside the workspace.";
  els.externalAuthNotice.append(heading, hint);

  let missing = false;
  for (const requirement of requirements) {
    const status = externalAuthStatus(requirement.id);
    const row = document.createElement("div");
    row.className = "external-auth-row";
    const label = document.createElement("span");
    label.textContent = status ? status.displayName || requirement.id : requirement.id;
    if (!requirement.optional) {
      const badge = document.createElement("span");
      badge.className = "external-auth-required";
      badge.textContent = "required";
      label.append(" ", badge);
    }
    const action = document.createElement("button");
    action.type = "button";
    action.className = "button small";
    if (status && status.authenticated) {
      action.textContent = "Connected";
      action.disabled = true;
    } else {
      action.textContent = "Connect";
      action.addEventListener("click", () => connectExternalAuth(requirement.id));
      if (!requirement.optional) missing = true;
    }
    row.append(label, action);
    els.externalAuthNotice.append(row);
  }
  els.externalAuthNotice.hidden = false;
  if (missing) {
    els.createSubmit.disabled = true;
    const warning = document.createElement("p");
    warning.className = "external-auth-warning";
    warning.textContent = "Connect the required accounts before creating this workspace.";
    els.externalAuthNotice.append(warning);
  } else if (!els.createSubmit.dataset.busy) {
    els.createSubmit.disabled = false;
  }
}
