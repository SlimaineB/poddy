"use strict";

function auditOutcomeClass(outcome) {
  switch (String(outcome || "").toLowerCase()) {
    case "success":
      return "success";
    case "denied":
      return "denied";
    case "error":
      return "danger";
    default:
      return "neutral";
  }
}
function auditDetails(event) {
  if (event.error) return event.error;
  const details = event.details || {};
  const parts = [];
  for (const [key, value] of Object.entries(details)) {
    if (value === null || value === undefined || value === "") continue;
    parts.push(`${key}=${typeof value === "string" ? value : JSON.stringify(value)}`);
  }
  return parts.join(", ");
}
function auditMatches(event, query) {
  if (!query) return true;
  const details = event.details ? JSON.stringify(event.details) : "";
  return [
    event.actor,
    event.actorType,
    event.action,
    event.outcome,
    event.targetType,
    event.targetName,
    event.targetNamespace,
    event.workspace,
    event.sourceIP,
    event.error,
    details,
  ].some((value) => String(value || "").toLowerCase().includes(query));
}
function renderAudit() {
  const query = state.auditQuery.trim().toLowerCase();
  const outcome = state.auditOutcome;
  const events = state.audit.filter((event) => {
    if (outcome && String(event.outcome || "").toLowerCase() !== outcome) return false;
    return auditMatches(event, query);
  });

  els.auditCount.textContent = `${events.length} event${events.length === 1 ? "" : "s"}`;
  els.auditList.replaceChildren();
  if (!events.length) {
    const row = document.createElement("tr");
    const cell = document.createElement("td");
    cell.colSpan = 7;
    cell.append(emptyInline(state.audit.length ? "No events match these filters." : "No audit events recorded yet."));
    row.append(cell);
    els.auditList.append(row);
    return;
  }

  for (const event of events) {
    const row = document.createElement("tr");

    const time = document.createElement("td");
    time.className = "audit-time";
    const occurred = new Date(event.occurredAt);
    time.textContent = Number.isNaN(occurred.getTime()) ? String(event.occurredAt || "") : occurred.toLocaleString();
    if (event.occurredAt) time.title = String(event.occurredAt);

    const actor = document.createElement("td");
    actor.className = "audit-actor";
    actor.textContent = event.actor || event.actorType || "—";
    if (event.actorType) actor.title = `actor type: ${event.actorType}`;

    const action = document.createElement("td");
    const actionCode = document.createElement("code");
    actionCode.textContent = event.action || "—";
    action.append(actionCode);

    const outcomeCell = document.createElement("td");
    const badge = document.createElement("span");
    badge.className = `badge ${auditOutcomeClass(event.outcome)}`;
    badge.textContent = event.outcome || "unknown";
    outcomeCell.append(badge);

    const target = document.createElement("td");
    target.className = "audit-target";
    const targetName = event.targetName || event.workspace || "";
    target.textContent = targetName ? `${event.targetType ? `${event.targetType}:` : ""}${targetName}` : "—";
    if (event.targetNamespace) target.title = `namespace: ${event.targetNamespace}`;

    const source = document.createElement("td");
    source.className = "audit-source";
    source.textContent = event.sourceIP || "—";
    if (event.userAgent) source.title = event.userAgent;

    const details = document.createElement("td");
    details.className = "audit-details";
    details.textContent = auditDetails(event) || "—";
    if (event.details) details.title = JSON.stringify(event.details, null, 2);
    if (event.requestId) details.title = `${details.title || ""}\nrequest: ${event.requestId}`.trim();

    row.append(time, actor, action, outcomeCell, target, source, details);
    els.auditList.append(row);
  }
}
async function loadAudit() {
  const data = await api(`/api/audit?limit=${encodeURIComponent(state.auditLimit)}`);
  state.audit = Array.isArray(data.events) ? data.events : [];
  renderAudit();
}
async function openAudit() {
  state.auditQuery = "";
  state.auditOutcome = "";
  state.auditLimit = 100;
  els.auditSearch.value = "";
  els.auditOutcome.value = "";
  els.auditLimit.value = "100";
  showView("audit");
  try {
    await loadAudit();
  } catch (err) {
    toast(err.message || "Failed to load audit log.");
  }
}
