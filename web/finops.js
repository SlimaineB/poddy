"use strict";

const finopsState = {
  request: 0,
  timer: 0,
};

const FINOPS_SIGNALS = {
  "idle-cpu": { label: "Idle CPU", tone: "warning" },
  "idle-memory": { label: "Idle memory", tone: "warning" },
  oversized: { label: "Oversized", tone: "danger" },
  contended: { label: "Contended", tone: "danger" },
  "missing-requests": { label: "No requests", tone: "warning" },
  "no-metrics": { label: "No metrics", tone: "neutral" },
  "not-running": { label: "Stopped", tone: "neutral" },
  restarts: { label: "Restarts", tone: "warning" },
  oom: { label: "OOM", tone: "danger" },
};

function canSeeFinOps() {
  return monitoringLiveEnabled();
}

function finopsCurrency(report) {
  return (report && report.pricing && report.pricing.currency) || "USD";
}

function formatMoney(value, currency) {
  if (value === undefined || value === null || !Number.isFinite(Number(value))) return "—";
  const number = Number(value);
  const decimals = Math.abs(number) >= 100 ? 0 : 2;
  return `${number.toFixed(decimals)} ${currency}`;
}

function finopsSignalBadge(key) {
  const meta = FINOPS_SIGNALS[key] || { label: key, tone: "neutral" };
  const badge = document.createElement("span");
  badge.className = `finops-signal tone-${meta.tone}`;
  badge.textContent = meta.label;
  return badge;
}

function finopsSignals(signals) {
  const wrap = document.createElement("span");
  wrap.className = "finops-signals";
  if (!Array.isArray(signals) || !signals.length) {
    const ok = document.createElement("span");
    ok.className = "finops-signal tone-ok";
    ok.textContent = "Healthy";
    wrap.append(ok);
    return wrap;
  }
  for (const signal of signals) wrap.append(finopsSignalBadge(signal));
  return wrap;
}

function finopsIdleText(item) {
  const cpu = item.idleCpuCores != null ? formatCPU(item.idleCpuCores) : "—";
  const memory = item.idleMemoryBytes != null ? formatBytes(item.idleMemoryBytes) : "—";
  return `${cpu} CPU · ${memory}`;
}

function finopsUtilCell(usage, request, formatter, unitLabel) {
  const cell = document.createElement("td");
  const wrap = document.createElement("div");
  wrap.className = "finops-util";
  const usedNode = document.createElement("span");
  usedNode.className = "finops-util-value";
  usedNode.textContent = formatter(usage);
  const track = document.createElement("div");
  track.className = "finops-track";
  const fill = document.createElement("span");
  const percent = usage != null && request != null && request > 0 ? (usage / request) * 100 : null;
  if (percent != null) {
    fill.style.width = `${Math.max(0, Math.min(100, percent))}%`;
    if (percent >= 90) fill.className = "danger";
    else if (percent >= 70) fill.className = "warning";
  } else {
    track.classList.add("empty");
  }
  track.append(fill);
  const requestNode = document.createElement("span");
  requestNode.className = "finops-muted";
  requestNode.textContent = request != null ? `of ${formatter(request)}` : "no request";
  requestNode.title = unitLabel;
  wrap.append(usedNode, track, requestNode);
  cell.append(wrap);
  return cell;
}

async function openFinOps() {
  if (!canSeeFinOps()) {
    showView("workspaces");
    return;
  }
  showView("finops");
  if (state.me && state.me.isAdmin) {
    try {
      await Promise.all([refreshTeams({ paginated: true, page: 1 }), loadDirectory()]);
    } catch {
      // Team attribution is best effort; the report still loads.
    }
  }
  if (!finopsState.timer) {
    finopsState.timer = setInterval(() => {
      if (!document.hidden && els.finopsView && !els.finopsView.hidden) {
        loadFinOps({ silent: true });
      }
    }, 30000);
  }
  await loadFinOps();
}

function closeFinOps() {
  clearInterval(finopsState.timer);
  finopsState.timer = 0;
  finopsState.request += 1;
  showView("workspaces");
}

async function loadFinOps({ silent = false } = {}) {
  if (!canSeeFinOps()) return;
  const request = ++finopsState.request;
  if (!silent) {
    els.finopsLoading.hidden = false;
    els.finopsEmpty.hidden = true;
  }
  let report;
  try {
    report = await api("/api/monitoring/finops");
  } catch (err) {
    if (request !== finopsState.request) return;
    els.finopsLoading.hidden = true;
    els.finopsEmpty.hidden = false;
    els.finopsEmptyText.textContent = err.message || "FinOps data is unavailable.";
    els.finopsListWrap.hidden = true;
    els.finopsKpis.replaceChildren();
    els.finopsMessages.replaceChildren();
    if (els.finopsOverviewPanel) els.finopsOverviewPanel.hidden = true;
    els.finopsBars.replaceChildren();
    if (!silent) toast(err.message || "Failed to load FinOps data.");
    return;
  }
  if (request !== finopsState.request) return;
  els.finopsLoading.hidden = true;
  renderFinOps(report);
}

function renderFinOps(report) {
  const rows = Array.isArray(report.workspaces) ? report.workspaces : [];
  const currency = finopsCurrency(report);

  els.finopsSubtitle.textContent = report.admin
    ? "Reserved resources versus real usage across every workspace, with per-team waste for managers."
    : "What your workspaces reserve, what they actually use, and where resources are wasted.";

  const bits = [];
  if (report.available) bits.push(report.source || "metrics-server");
  if (report.observedAt) bits.push(`updated ${formatAge(report.observedAt)}`);
  if (report.reason && !report.available) bits.push(report.reason);
  els.finopsUpdated.textContent = bits.join(" · ") || "No live data";

  renderFinOpsKpis(report, currency);
  renderFinOpsMessages(report, currency);
  renderFinOpsOverview(report, currency);
  renderFinOpsBars(report, currency);
  renderFinOpsTeams(report, currency);
  renderFinOpsTable(report, currency);

  const hasRows = rows.length > 0;
  els.finopsEmpty.hidden = hasRows || !report.available;
  els.finopsEmptyText.textContent = report.reason || "No workspaces are reporting live metrics yet.";
  els.finopsListWrap.hidden = !hasRows;
}

function renderFinOpsKpis(report, currency) {
  const totals = report.totals || {};
  let cards;
  if (report.costVisible) {
    cards = [
      kpiCard({
        label: "Reserved / hour",
        value: formatMoney(totals.reservedHourly, currency),
        meta: "at current requests",
      }),
      kpiCard({
        label: "Used / hour",
        value: formatMoney(totals.usedHourly, currency),
        meta: `${formatCPU(totals.cpuUsageCores)} CPU · ${formatBytes(totals.memoryUsageBytes)}`,
      }),
      kpiCard({
        label: "Wasted / hour",
        value: formatMoney(totals.wastedHourly, currency),
        meta: `${formatCPU(totals.cpuIdleCores)} CPU · ${formatBytes(totals.memoryIdleBytes)} idle`,
        tone: totals.wastedHourly > 0 ? "warning" : "",
        progress: totals.reservedHourly > 0 ? totals.wastedHourly / totals.reservedHourly : null,
      }),
      kpiCard({
        label: "Waste ratio",
        value: `${Number(totals.wastePercent || 0).toFixed(1)}%`,
        meta: totals.idle ? `${totals.idle} idle workspace${totals.idle === 1 ? "" : "s"}` : "No idle workspaces",
        tone: totals.wastePercent >= 60 ? "warning" : "",
      }),
      kpiCard({
        label: "Storage / month",
        value: formatMoney(totals.storageMonthly, currency),
        meta: `${formatBytes(totals.storageBytes)} provisioned`,
      }),
    ];
  } else {
    cards = [
      kpiCard({
        label: "CPU requested",
        value: formatCPU(totals.cpuRequestsCores),
        meta: `${formatCPU(totals.cpuUsageCores)} actually used`,
      }),
      kpiCard({
        label: "CPU idle",
        value: formatCPU(totals.cpuIdleCores),
        meta: totals.cpuRequestsCores > 0 ? `${((totals.cpuIdleCores / totals.cpuRequestsCores) * 100).toFixed(0)}% of requests` : "No requests",
        tone: totals.cpuIdleCores > 0 ? "warning" : "",
        progress: totals.cpuRequestsCores > 0 ? totals.cpuIdleCores / totals.cpuRequestsCores : null,
      }),
      kpiCard({
        label: "Memory requested",
        value: formatBytes(totals.memoryRequestsBytes),
        meta: `${formatBytes(totals.memoryUsageBytes)} actually used`,
      }),
      kpiCard({
        label: "Memory idle",
        value: formatBytes(totals.memoryIdleBytes),
        meta: totals.memoryRequestsBytes > 0 ? `${((totals.memoryIdleBytes / totals.memoryRequestsBytes) * 100).toFixed(0)}% of requests` : "No requests",
        tone: totals.memoryIdleBytes > 0 ? "warning" : "",
        progress: totals.memoryRequestsBytes > 0 ? totals.memoryIdleBytes / totals.memoryRequestsBytes : null,
      }),
    ];
  }
  els.finopsKpis.replaceChildren(...cards);
}

function renderFinOpsMessages(report, currency) {
  els.finopsMessages.replaceChildren();
  const totals = report.totals || {};
  const messages = [];
  const add = (tone, text) => messages.push({ tone, text });

  if (!report.available && report.reason) {
    add("neutral", report.reason);
  }
  if (report.costVisible) {
    if (totals.wastePercent >= 60) {
      add("danger", `${totals.wastePercent.toFixed(0)}% of what you reserve is idle. Lower CPU/memory requests on the workspaces below to stop paying for unused capacity.`);
    } else if (totals.wastePercent >= 30) {
      add("warning", `${totals.wastePercent.toFixed(0)}% of reserved compute is idle. A few request adjustments would recover ${formatMoney(totals.wastedHourly * 730, currency)} per month.`);
    }
  } else {
    if (totals.cpuIdleCores > 0 || totals.memoryIdleBytes > 0) {
      add("warning", `Idle reservations: ${formatCPU(totals.cpuIdleCores)} CPU and ${formatBytes(totals.memoryIdleBytes)} memory are requested but unused.`);
    }
    add("neutral", "Costs are hidden. Set the PODDY_COST_* prices and enable PODDY_MONITORING_COSTS to show estimated spend.");
  }
  if (totals.oversized) {
    add("warning", `${totals.oversized} workspace${totals.oversized === 1 ? " is" : "s are"} oversized: requests are far above real usage.`);
  }
  if (totals.missingRequests) {
    add("neutral", `${totals.missingRequests} workspace${totals.missingRequests === 1 ? " has" : "s have"} no CPU/memory requests, so usage cannot be attributed or costed.`);
  }
  if (totals.restarts) {
    add("warning", `${totals.restarts} container restart${totals.restarts === 1 ? "" : "s"} detected. Investigate OOM kills and crash loops.`);
  }

  if (!messages.length) {
    add("ok", "Usage is well aligned with reservations. Nothing to fix right now.");
  }
  for (const message of messages) {
    const note = document.createElement("p");
    note.className = `finops-message tone-${message.tone}`;
    note.textContent = message.text;
    els.finopsMessages.append(note);
  }
}

function finopsUsagePercent(used, reserved) {
  if (!(reserved > 0)) return null;
  return Math.max(0, Math.min(100, (used / reserved) * 100));
}

function finopsOverviewRow(label, used, reserved, formatter) {
  const percent = finopsUsagePercent(used, reserved);
  const idle = Math.max(0, reserved - used);
  const idlePercent = percent == null ? null : 100 - percent;
  const tone = idlePercent == null ? "empty" : idlePercent >= 60 ? "danger" : idlePercent >= 30 ? "warning" : "ok";

  const row = document.createElement("article");
  row.className = "finops-resource";

  const head = document.createElement("div");
  head.className = "finops-resource-head";
  const name = document.createElement("span");
  name.className = "finops-resource-label";
  name.textContent = label;
  const value = document.createElement("span");
  value.className = "finops-resource-value";
  value.textContent = reserved > 0
    ? `${formatter(used)} used of ${formatter(reserved)}`
    : "No reservation";
  head.append(name, value);

  const track = document.createElement("div");
  track.className = `finops-track large${tone === "empty" ? " empty" : ""}`;
  const fill = document.createElement("span");
  fill.style.width = `${percent == null ? 0 : Math.max(1, percent)}%`;
  if (tone === "warning" || tone === "danger") fill.className = tone;
  track.append(fill);

  const foot = document.createElement("div");
  foot.className = "finops-resource-foot";
  const usedLabel = document.createElement("span");
  usedLabel.textContent = percent == null ? "Usage cannot be compared" : `${percent.toFixed(0)}% used`;
  const idleLabel = document.createElement("span");
  idleLabel.className = `finops-resource-idle tone-${tone}`;
  idleLabel.textContent = percent == null ? "Add requests to measure waste" : `${formatter(idle)} idle`;
  foot.append(usedLabel, idleLabel);

  row.append(head, track, foot);
  return row;
}

function renderFinOpsOverview(report, currency) {
  const panel = els.finopsOverviewPanel;
  if (!panel) return;
  const totals = report.totals || {};
  const hasReservations = Number(totals.cpuRequestsCores) > 0 || Number(totals.memoryRequestsBytes) > 0;
  if (!report.available || !hasReservations) {
    panel.hidden = true;
    return;
  }
  panel.hidden = false;

  els.finopsOverviewNote.textContent = report.costVisible
    ? `${formatMoney(totals.usedHourly, currency)}/h used · ${formatMoney(totals.wastedHourly, currency)}/h idle`
    : `${formatCPU(totals.cpuUsageCores)} CPU · ${formatBytes(totals.memoryUsageBytes)} in use`;
  els.finopsOverviewCopy.textContent = report.costVisible
    ? "Each bar compares live usage with the resources reserved by workspace requests. The empty part is capacity you pay for but do not use."
    : "Each bar compares live usage with the resources reserved by workspace requests. The empty part is capacity blocked from other workspaces, even when no prices are configured.";

  els.finopsOverview.replaceChildren(
    finopsOverviewRow("CPU", Number(totals.cpuUsageCores) || 0, Number(totals.cpuRequestsCores) || 0, formatCPU),
    finopsOverviewRow("Memory", Number(totals.memoryUsageBytes) || 0, Number(totals.memoryRequestsBytes) || 0, formatBytes),
  );

  const stats = [
    [totals.workspaces || 0, "workspaces"],
    [totals.idle || 0, "idle"],
    [totals.oversized || 0, "oversized"],
    [totals.missingRequests || 0, "no requests"],
    [report.costVisible ? formatMoney(totals.storageMonthly, currency) : formatBytes(totals.storageBytes), report.costVisible ? "storage / month" : "storage provisioned"],
  ];
  els.finopsOverviewStats.replaceChildren(...stats.map(([value, label]) => {
    const item = document.createElement("div");
    item.className = "finops-overview-stat";
    const strong = document.createElement("strong");
    strong.textContent = value;
    const span = document.createElement("span");
    span.textContent = label;
    item.append(strong, span);
    return item;
  }));
}

function renderFinOpsBars(report, currency) {
  const rows = (Array.isArray(report.workspaces) ? report.workspaces : [])
    .filter((item) => item.wasteScore > 0)
    .sort((a, b) => b.wasteScore - a.wasteScore)
    .slice(0, 8);
  els.finopsBars.replaceChildren();
  if (!rows.length) {
    els.finopsBarsNote.textContent = "No waste detected";
    const note = document.createElement("p");
    note.className = "finops-muted";
    note.textContent = "No workspace is reserving resources it does not use.";
    els.finopsBars.append(note);
    return;
  }
  els.finopsBarsNote.textContent = `${rows.length} workspace${rows.length === 1 ? "" : "s"}`;
  const max = Math.max(...rows.map((item) => item.wasteScore), 0);
  for (const item of rows) {
    const row = document.createElement("div");
    row.className = "finops-bar-row";

    const head = document.createElement("div");
    head.className = "finops-bar-head";
    const name = document.createElement("span");
    name.className = "finops-bar-name";
    name.textContent = item.name;
    const value = document.createElement("span");
    value.className = "finops-bar-value";
    value.textContent = report.costVisible
      ? `${formatMoney(item.wastedHourly, currency)}/h`
      : finopsIdleText(item);
    head.append(name, value);

    const track = document.createElement("div");
    track.className = "finops-track large";
    const fill = document.createElement("span");
    fill.style.width = `${max > 0 ? Math.max(4, Math.round((item.wasteScore / max) * 100)) : 0}%`;
    track.append(fill);

    const foot = document.createElement("div");
    foot.className = "finops-bar-foot";
    foot.append(finopsSignals(item.signals));

    row.append(head, track, foot);
    els.finopsBars.append(row);
  }
}

function finopsOwnerKey(value) {
  return String(value || "").trim().toLowerCase();
}

function renderFinOpsTeams(report, currency) {
  if (!els.finopsTeamsPanel) return;
  const show = Boolean(report.admin);
  els.finopsTeamsPanel.hidden = !show;
  if (!show) return;
  els.finopsTeams.replaceChildren();

  const owners = new Map();
  for (const item of report.workspaces || []) {
    const key = finopsOwnerKey(item.owner) || "unknown";
    let entry = owners.get(key);
    if (!entry) {
      entry = {
        owner: item.owner || "Unknown owner",
        wasteScore: 0,
        wastedHourly: 0,
        idleCpuCores: 0,
        idleMemoryBytes: 0,
        workspaces: 0,
        signals: new Set(),
        teams: new Set(),
      };
      owners.set(key, entry);
    }
    entry.wasteScore += Number(item.wasteScore) || 0;
    entry.wastedHourly += Number(item.wastedHourly) || 0;
    entry.idleCpuCores += Number(item.idleCpuCores) || 0;
    entry.idleMemoryBytes += Number(item.idleMemoryBytes) || 0;
    entry.workspaces += 1;
    for (const signal of item.signals || []) entry.signals.add(signal);
    for (const team of item.teams || []) entry.teams.add(team);
  }

  const teams = new Map();
  const unassigned = [];
  for (const entry of owners.values()) {
    if (!entry.teams.size) {
      unassigned.push(entry);
      continue;
    }
    for (const team of entry.teams) {
      if (!teams.has(team)) teams.set(team, []);
      teams.get(team).push(entry);
    }
  }

  const names = [...teams.keys()].sort((a, b) => a.localeCompare(b));
  for (const name of names) {
    const members = teams.get(name).sort((a, b) => b.wasteScore - a.wasteScore);
    els.finopsTeams.append(finopsTeamBlock(name, members, currency, report.costVisible));
  }
  if (unassigned.length) {
    const members = unassigned.sort((a, b) => b.wasteScore - a.wasteScore);
    els.finopsTeams.append(finopsTeamBlock("No team", members, currency, report.costVisible, true));
  }
  if (!names.length && !unassigned.length) {
    const note = document.createElement("p");
    note.className = "finops-muted";
    note.textContent = "No team memberships to rank.";
    els.finopsTeams.append(note);
  }
}

function finopsTeamBlock(name, members, currency, costVisible, muted = false) {
  const block = document.createElement("article");
  block.className = "finops-team";
  const head = document.createElement("div");
  head.className = "finops-team-head";
  const title = document.createElement("h3");
  title.textContent = name;
  const totalHourly = members.reduce((sum, member) => sum + member.wastedHourly, 0);
  const summary = document.createElement("span");
  summary.className = "finops-team-total";
  summary.textContent = costVisible ? `${formatMoney(totalHourly, currency)}/h wasted` : `${members.length} member${members.length === 1 ? "" : "s"}`;
  head.append(title, summary);
  block.append(head);
  if (muted) {
    const note = document.createElement("p");
    note.className = "finops-muted";
    note.textContent = "Owners that are not in a Poddy team.";
    block.append(note);
  }
  const max = Math.max(...members.map((member) => member.wasteScore), 0);
  for (const member of members) {
    const row = document.createElement("div");
    row.className = "finops-member-row";
    const info = document.createElement("div");
    info.className = "finops-member-info";
    const nameNode = document.createElement("span");
    nameNode.className = "finops-member-name";
    nameNode.textContent = member.owner;
    const count = document.createElement("span");
    count.className = "finops-muted";
    count.textContent = `${member.workspaces} workspace${member.workspaces === 1 ? "" : "s"}`;
    info.append(nameNode, count);
    const value = document.createElement("span");
    value.className = "finops-member-value";
    value.textContent = costVisible
      ? `${formatMoney(member.wastedHourly, currency)}/h`
      : `${formatCPU(member.idleCpuCores)} CPU · ${formatBytes(member.idleMemoryBytes)}`;
    row.append(info, value);
    const track = document.createElement("div");
    track.className = "finops-track";
    const fill = document.createElement("span");
    fill.style.width = `${max > 0 ? Math.max(3, Math.round((member.wasteScore / max) * 100)) : 0}%`;
    track.append(fill);
    row.append(track);
    block.append(row);
  }
  return block;
}

function renderFinOpsTable(report, currency) {
  els.finopsList.replaceChildren();
  const rows = (Array.isArray(report.workspaces) ? report.workspaces : [])
    .slice()
    .sort((a, b) => b.wasteScore - a.wasteScore);
  for (const item of rows) {
    const tr = document.createElement("tr");

    const nameCell = document.createElement("td");
    const name = document.createElement("button");
    name.type = "button";
    name.className = "workspace-name-button";
    name.textContent = item.name;
    name.addEventListener("click", () => showDetails({
      name: item.name,
      template: item.template,
      phase: item.phase,
      owner: item.owner,
      region: item.region,
    }));
    nameCell.append(name);
    tr.append(nameCell);

    const ownerCell = document.createElement("td");
    ownerCell.className = "finops-owner";
    ownerCell.textContent = item.owner || "—";
    if (Array.isArray(item.teams) && item.teams.length) {
      const teams = document.createElement("span");
      teams.className = "finops-owner-teams";
      teams.textContent = item.teams.join(", ");
      ownerCell.append(teams);
    }
    tr.append(ownerCell);

    tr.append(finopsUtilCell(
      item.usage && item.usage.cpuCores,
      item.requests && item.requests.cpuCores,
      formatCPU,
      "CPU usage versus request",
    ));
    tr.append(finopsUtilCell(
      item.usage && item.usage.memoryBytes,
      item.requests && item.requests.memoryBytes,
      formatBytes,
      "Memory usage versus request",
    ));

    const wasteCell = document.createElement("td");
    wasteCell.className = "finops-waste";
    if (report.costVisible) {
      wasteCell.textContent = `${formatMoney(item.wastedHourly, currency)}/h`;
    } else {
      wasteCell.textContent = finopsIdleText(item);
    }
    tr.append(wasteCell);

    const signalCell = document.createElement("td");
    signalCell.append(finopsSignals(item.signals));
    tr.append(signalCell);

    const costCell = document.createElement("td");
    costCell.className = "finops-muted";
    if (report.costVisible) {
      const parts = [`${formatMoney(item.reservedHourly, currency)}/h`];
      if (item.storageMonthly != null) parts.push(`${formatMoney(item.storageMonthly, currency)}/mo disk`);
      costCell.textContent = parts.join(" + ");
    } else {
      costCell.textContent = "—";
    }
    tr.append(costCell);

    const actionCell = document.createElement("td");
    actionCell.className = "workspace-table-actions";
    const details = document.createElement("button");
    details.type = "button";
    details.className = "button ghost small";
    details.textContent = "Details";
    details.addEventListener("click", () => showDetails({
      name: item.name,
      template: item.template,
      phase: item.phase,
      owner: item.owner,
      region: item.region,
    }));
    actionCell.append(details);
    tr.append(actionCell);

    els.finopsList.append(tr);
  }
}

if (els.manageFinops) els.manageFinops.addEventListener("click", openFinOps);
if (els.finopsBack) els.finopsBack.addEventListener("click", closeFinOps);
if (els.finopsRefresh) els.finopsRefresh.addEventListener("click", () => loadFinOps());
