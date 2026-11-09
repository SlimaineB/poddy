"use strict";

function auditURL(page = state.auditPage) {
  const params = new URLSearchParams({
    page: String(page),
    per_page: String(state.auditPerPage),
  });
  if (state.auditQuery) params.set("q", state.auditQuery);
  if (state.auditOutcome) params.set("outcome", state.auditOutcome);
  if (state.auditAction) params.set("action", state.auditAction);
  if (state.auditSince) params.set("since", state.auditSince);
  if (state.auditUntil) params.set("until", state.auditUntil);
  return `/api/audit?${params.toString()}`;
}

async function loadAudit({ silent = false } = {}) {
  if (!silent) {
    els.auditLoading.hidden = false;
    els.auditEmpty.hidden = true;
    els.auditListWrap.hidden = true;
  }
  try {
    const data = await api(auditURL());
    state.auditItems = Array.isArray(data.items) ? data.items : [];
    state.auditTotal = Number(data.total) || 0;
    state.auditPage = Number(data.page) || 1;
    state.auditPerPage = Number(data.perPage) || state.auditPerPage;
    state.auditPages = Number(data.pages) || 0;
    renderAudit();
  } catch (err) {
    state.auditItems = [];
    state.auditTotal = 0;
    state.auditPages = 0;
    renderAudit();
    toast(err.message || "Failed to load audit events.");
  } finally {
    els.auditLoading.hidden = true;
  }
}

function renderAudit() {
  els.auditCount.textContent = `${state.auditTotal} event${state.auditTotal === 1 ? "" : "s"}`;
  els.auditList.replaceChildren();
  els.auditEmpty.hidden = state.auditItems.length > 0;
  els.auditListWrap.hidden = state.auditItems.length === 0;
  for (const event of state.auditItems) {
    const row = document.createElement("tr");
    row.title = event.userAgent ? `User-Agent: ${event.userAgent}` : "";

    const time = document.createElement("td");
    time.className = "audit-time";
    time.textContent = event.occurredAt ? new Date(event.occurredAt).toLocaleString() : "";
    row.append(time);

    const actor = document.createElement("td");
    const actorName = document.createElement("div");
    actorName.className = "audit-actor";
    actorName.textContent = event.actor || "system";
    const actorType = document.createElement("span");
    actorType.className = "badge neutral";
    actorType.textContent = event.actorType || "system";
    actor.append(actorName, actorType);
    row.append(actor);

    const action = document.createElement("td");
    action.className = "audit-action";
    action.textContent = event.action || "";
    row.append(action);

    const outcome = document.createElement("td");
    const badge = document.createElement("span");
    badge.className = `badge audit-outcome ${event.outcome || ""}`;
    badge.textContent = event.outcome || "unknown";
    outcome.append(badge);
    row.append(outcome);

    const target = document.createElement("td");
    target.className = "audit-target";
    const targetName = event.targetName || "—";
    target.textContent = event.targetType ? `${event.targetType}: ${targetName}` : targetName;
    if (event.targetNamespace) {
      const namespace = document.createElement("span");
      namespace.className = "audit-muted";
      namespace.textContent = ` (${event.targetNamespace})`;
      target.append(namespace);
    }
    row.append(target);

    const workspace = document.createElement("td");
    workspace.className = "audit-workspace";
    workspace.textContent = event.workspace || "—";
    row.append(workspace);

    const source = document.createElement("td");
    source.className = "audit-source";
    source.textContent = event.sourceIP || "—";
    row.append(source);

    const details = document.createElement("td");
    details.className = "audit-details";
    const hasDetails = Boolean(event.details && Object.keys(event.details).length);
    if (hasDetails) {
      const disclosure = document.createElement("details");
      const summary = document.createElement("summary");
      summary.textContent = "Details";
      const pre = document.createElement("pre");
      pre.textContent = JSON.stringify(event.details, null, 2);
      disclosure.append(summary, pre);
      details.append(disclosure);
    }
    if (event.error) {
      const error = document.createElement("div");
      error.className = "audit-error";
      error.textContent = event.error;
      details.append(error);
    }
    if (!hasDetails && !event.error) details.textContent = "—";
    row.append(details);

    els.auditList.append(row);
  }
  renderPagination(els.auditPagination, {
    page: state.auditPage,
    pages: state.auditPages,
    total: state.auditTotal,
    summary: `${state.auditTotal} event${state.auditTotal === 1 ? "" : "s"}`,
  }, (page) => {
    state.auditPage = page;
    loadAudit();
  });
}

function auditFilters() {
  state.auditQuery = els.auditSearch.value.trim();
  state.auditOutcome = els.auditOutcome.value;
  state.auditAction = els.auditAction.value.trim();
  state.auditSince = els.auditSince.value;
  state.auditUntil = els.auditUntil.value;
  state.auditPage = 1;
  loadAudit();
}

function clearAuditFilters() {
  els.auditSearch.value = "";
  els.auditOutcome.value = "";
  els.auditAction.value = "";
  els.auditSince.value = "";
  els.auditUntil.value = "";
  auditFilters();
}

async function openAudit() {
  state.auditPage = 1;
  state.auditQuery = "";
  state.auditOutcome = "";
  state.auditAction = "";
  state.auditSince = "";
  state.auditUntil = "";
  els.auditSearch.value = "";
  els.auditOutcome.value = "";
  els.auditAction.value = "";
  els.auditSince.value = "";
  els.auditUntil.value = "";
  showView("audit");
  await loadAudit();
}

els.auditApply.addEventListener("click", auditFilters);
els.auditClear.addEventListener("click", clearAuditFilters);
els.auditRefresh.addEventListener("click", () => loadAudit());
let auditSearchTimer = 0;
els.auditSearch.addEventListener("input", () => {
  clearTimeout(auditSearchTimer);
  auditSearchTimer = setTimeout(auditFilters, 250);
});
els.auditOutcome.addEventListener("change", auditFilters);
