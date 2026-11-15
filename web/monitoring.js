"use strict";

const monitoringState = {
  range: "1h",
  detailRange: "1h",
  request: 0,
  detailRequest: 0,
  timer: 0,
  detailTimer: 0,
};

let chartSequence = 0;

function monitoringConfig() {
  return (state.config && state.config.monitoring) || {};
}

function monitoringEnabled() {
  return Boolean(monitoringConfig().enabled);
}

function monitoringLiveEnabled() {
  return monitoringEnabled() && Boolean(monitoringConfig().live);
}

function monitoringHistoryEnabled() {
  return monitoringEnabled() && Boolean(monitoringConfig().history);
}

function monitoringGlobalEnabled() {
  return monitoringEnabled() && Boolean(monitoringConfig().global);
}

function canSeeWorkspaceMonitoring() {
  return monitoringEnabled() && (monitoringLiveEnabled() || monitoringHistoryEnabled());
}

function canSeeGlobalMonitoring() {
  return monitoringGlobalEnabled() && Boolean(state.me && state.me.isAdmin);
}

function renderMonitoringConfig() {
  if (els.manageMonitoring) {
    els.manageMonitoring.hidden = !canSeeGlobalMonitoring();
  }
  if (typeof syncManageMenu === "function") syncManageMenu();
  if (els.detailMonitoring) {
    els.detailMonitoring.hidden = !canSeeWorkspaceMonitoring();
  }
  if (els.detailMonitoringNote) {
    const live = monitoringLiveEnabled();
    const history = monitoringHistoryEnabled();
    els.detailMonitoringNote.textContent = live && history
      ? "Live usage from metrics-server and historical trends from Prometheus."
      : live
        ? "Live CPU and memory usage from metrics-server."
        : history
          ? "Historical resource trends from Prometheus."
          : "Monitoring is disabled.";
  }
}

async function openMonitoring() {
  if (!canSeeGlobalMonitoring()) {
    showView("workspaces");
    return;
  }
  showView("monitoring");
  if (!monitoringState.timer) {
    monitoringState.timer = setInterval(() => {
      if (!document.hidden && els.monitoringView && !els.monitoringView.hidden) {
        loadMonitoring({ silent: true });
      }
    }, 15000);
  }
  await loadMonitoring();
}

function closeMonitoring() {
  clearInterval(monitoringState.timer);
  monitoringState.timer = 0;
  monitoringState.request += 1;
  showView("workspaces");
}

async function loadMonitoring({ silent = false } = {}) {
  if (!canSeeGlobalMonitoring()) return;
  const request = ++monitoringState.request;
  if (!silent) {
    els.monitoringLoading.hidden = false;
    els.monitoringEmpty.hidden = true;
  }

  const historyPromise = monitoringHistoryEnabled()
    ? api(`/api/monitoring/history?range=${encodeURIComponent(monitoringState.range)}`)
    : Promise.resolve({ available: false, reason: "History is disabled", series: [] });
  const [overviewResult, historyResult] = await Promise.allSettled([
    api("/api/monitoring/overview"),
    historyPromise,
  ]);
  if (request !== monitoringState.request) return;

  els.monitoringLoading.hidden = true;
  const overview = overviewResult.status === "fulfilled"
    ? overviewResult.value
    : { available: false, reason: overviewResult.reason.message || "Monitoring is unavailable", workspaces: [] };
  const history = historyResult.status === "fulfilled"
    ? historyResult.value
    : { available: false, reason: historyResult.reason.message || "Prometheus is unavailable", series: [] };

  if (overviewResult.status === "rejected" && !silent) {
    toast(overviewResult.reason.message || "Failed to load monitoring data.");
  }

  renderMonitoringSummary(overview);
  renderMonitoringCharts(history, els.monitoringCharts);
  renderMonitoringTable(overview);
  renderMonitoringSource(overview, history);
}

function renderMonitoringSource(overview, history) {
  if (!els.monitoringSource) return;
  const bits = [];
  if (overview && overview.source) bits.push(`Live: ${overview.source}`);
  if (history && history.available) bits.push(`History: ${history.source || "Prometheus"} (${monitoringState.range})`);
  if (overview && overview.observedAt) bits.push(`Updated ${formatAge(overview.observedAt)}`);
  els.monitoringSource.textContent = bits.join(" · ") || "Monitoring data unavailable.";
  if (els.monitoringUpdated) {
    els.monitoringUpdated.textContent = overview && overview.available
      ? `${overview.workspaces.length} workspaces`
      : "No live metrics";
  }
}

function renderMonitoringSummary(overview) {
  const rows = Array.isArray(overview.workspaces) ? overview.workspaces : [];
  const running = rows.filter((row) => row.phase === "Running").length;
  const restarts = rows.reduce((sum, row) => sum + (Number(row.restarts) || 0), 0);
  const cards = [
    kpiCard({
      label: "Workspaces",
      value: `${running} / ${rows.length}`,
      meta: `${rows.length - running} not running`,
    }),
    kpiCard({
      label: "CPU",
      value: formatCPU(overview.usage && overview.usage.cpuCores),
      meta: `${formatCPU(overview.requests && overview.requests.cpuCores)} requested`,
      progress: ratio(overview.usage && overview.usage.cpuCores, overview.requests && overview.requests.cpuCores),
    }),
    kpiCard({
      label: "Memory",
      value: formatBytes(overview.usage && overview.usage.memoryBytes),
      meta: `${formatBytes(overview.requests && overview.requests.memoryBytes)} requested`,
      progress: ratio(overview.usage && overview.usage.memoryBytes, overview.requests && overview.requests.memoryBytes),
    }),
    kpiCard({
      label: "Restarts",
      value: String(restarts),
      meta: restarts ? "Containers restarted" : "No restarts",
      tone: restarts ? "warning" : "",
    }),
  ];
  els.monitoringSummary.replaceChildren(...cards);
  if (overview.reason && !overview.available) {
    const note = document.createElement("p");
    note.className = "monitoring-note";
    note.textContent = overview.reason;
    els.monitoringSummary.append(note);
  }
}

function renderMonitoringCharts(history, container) {
  if (!container) return;
  container.replaceChildren();
  if (!monitoringHistoryEnabled()) {
    appendMonitoringNote(container, "Historical charts are disabled.");
    return;
  }
  if (!history || !history.available) {
    appendMonitoringNote(container, (history && history.reason) || "Prometheus history is unavailable.");
    return;
  }
  const series = Array.isArray(history.series) ? history.series : [];
  if (!series.length) {
    appendMonitoringNote(container, "Prometheus is configured, but no historical series were returned.");
    return;
  }
  const order = ["cpu", "memory", "storageUsed", "storageCapacity", "fsRead", "fsWrite", "fsReadIOPS", "fsWriteIOPS", "networkRx", "networkTx", "networkErrors", "networkDrops", "cpuThrottle", "restarts"];
  series.sort((a, b) => order.indexOf(a.key) - order.indexOf(b.key));
  container.append(...series.map((item) => chartCard(item)));
}

function renderMonitoringTable(overview) {
  const rows = Array.isArray(overview.workspaces) ? overview.workspaces.slice() : [];
  const hasRows = rows.length > 0;
  els.monitoringEmpty.hidden = hasRows;
  els.monitoringListWrap.hidden = !hasRows;
  els.monitoringList.replaceChildren();
  if (!hasRows) return;

  rows.sort((a, b) => {
    const cpuA = a.usage && a.usage.cpuCores != null ? a.usage.cpuCores : -1;
    const cpuB = b.usage && b.usage.cpuCores != null ? b.usage.cpuCores : -1;
    if (cpuA !== cpuB) return cpuB - cpuA;
    return String(a.name).localeCompare(String(b.name));
  });

  for (const row of rows) {
    const tr = document.createElement("tr");

    const nameCell = document.createElement("td");
    const name = document.createElement("button");
    name.type = "button";
    name.className = "workspace-name-button";
    name.textContent = row.name;
    name.addEventListener("click", () => showDetails({
      name: row.name,
      template: row.template,
      phase: row.phase,
      owner: row.owner,
      region: row.region,
    }));
    nameCell.append(name);
    tr.append(nameCell);

    const statusCell = document.createElement("td");
    statusCell.append(statusBadge(row.phase || "Unknown"));
    tr.append(statusCell);

    tr.append(textCell(row.region || "—"));
    tr.append(usageCell(row.usage && row.usage.cpuCores, row.requests && row.requests.cpuCores, formatCPU, "cpu"));
    tr.append(usageCell(row.usage && row.usage.memoryBytes, row.requests && row.requests.memoryBytes, formatBytes, "memory"));

    const requestCell = document.createElement("td");
    requestCell.className = "monitoring-muted";
    requestCell.textContent = `${formatCPU(row.requests && row.requests.cpuCores)} · ${formatBytes(row.requests && row.requests.memoryBytes)}`;
    tr.append(requestCell);

    const restartCell = textCell(String(row.restarts || 0), row.restarts ? "monitoring-warning" : "monitoring-muted");
    tr.append(restartCell);

    const updatedCell = document.createElement("td");
    updatedCell.className = "monitoring-muted";
    updatedCell.textContent = row.observedAt ? formatAge(row.observedAt) : (row.reason || "—");
    tr.append(updatedCell);

    const actionCell = document.createElement("td");
    actionCell.className = "workspace-table-actions";
    const details = document.createElement("button");
    details.type = "button";
    details.className = "button ghost small";
    details.textContent = "Details";
    details.addEventListener("click", () => showDetails({
      name: row.name,
      template: row.template,
      phase: row.phase,
      owner: row.owner,
      region: row.region,
    }));
    actionCell.append(details);
    tr.append(actionCell);
    els.monitoringList.append(tr);
  }
}

function resetWorkspaceMonitoring() {
  monitoringState.detailRequest += 1;
  if (!els.detailMonitoring) return;
  const visible = canSeeWorkspaceMonitoring();
  els.detailMonitoring.hidden = !visible;
  if (!visible) {
    stopWorkspaceMonitoring();
    return;
  }
  els.detailMonitoringBody.replaceChildren();
  appendMonitoringNote(els.detailMonitoringBody, "Loading workspace metrics...");
  if (els.detailMonitoringNote) els.detailMonitoringNote.textContent = "Loading live usage and historical trends.";
}

async function loadWorkspaceMonitoring(workspace, { silent = false } = {}) {
  if (!workspace || !canSeeWorkspaceMonitoring()) return;
  const request = ++monitoringState.detailRequest;
  const name = encodeURIComponent(workspace.name);
  const livePromise = monitoringLiveEnabled()
    ? api(`/api/workspaces/${name}/metrics`)
    : Promise.resolve({ available: false, reason: "Live metrics are disabled", containers: [] });
  const historyPromise = monitoringHistoryEnabled()
    ? api(`/api/workspaces/${name}/metrics/history?range=${encodeURIComponent(monitoringState.detailRange)}`)
    : Promise.resolve({ available: false, reason: "History is disabled", series: [] });

  const [liveResult, historyResult] = await Promise.allSettled([livePromise, historyPromise]);
  if (request !== monitoringState.detailRequest || state.detailWorkspace?.name !== workspace.name) return;

  const live = liveResult.status === "fulfilled"
    ? liveResult.value
    : { available: false, reason: liveResult.reason.message || "Live metrics are unavailable", containers: [] };
  const history = historyResult.status === "fulfilled"
    ? historyResult.value
    : { available: false, reason: historyResult.reason.message || "Prometheus is unavailable", series: [] };

  if (!silent && liveResult.status === "rejected" && monitoringLiveEnabled()) {
    toast(liveResult.reason.message || "Failed to load live workspace metrics.");
  }
  renderWorkspaceMonitoring(workspace, live, history);
  startDetailMonitoringTimer();
}

function renderWorkspaceMonitoring(workspace, live, history) {
  const body = els.detailMonitoringBody;
  if (!body) return;
  body.replaceChildren();

  const cpuStats = seriesStats(history, "cpu");
  const memoryStats = seriesStats(history, "memory");
  const storageUsed = seriesStats(history, "storageUsed");
  const storageCapacity = seriesStats(history, "storageCapacity");
  const networkRx = seriesStats(history, "networkRx");
  const networkTx = seriesStats(history, "networkTx");
  const fsReadIOPS = seriesStats(history, "fsReadIOPS");
  const fsWriteIOPS = seriesStats(history, "fsWriteIOPS");
  const restarts = Array.isArray(live.containers)
    ? live.containers.reduce((sum, container) => sum + (Number(container.restarts) || 0), 0)
    : 0;
  const oom = Array.isArray(live.containers) && live.containers.some((container) => container.oomKilled);
  const ready = Array.isArray(live.containers) && live.containers.length > 0 && live.containers.every((container) => container.ready);

  const kpis = document.createElement("section");
  kpis.className = "monitoring-pro-kpis";
  kpis.append(
    kpiCard({
      label: "CPU",
      value: formatCPU(live.usage && live.usage.cpuCores),
      meta: requestLimitMeta(live.requests && live.requests.cpuCores, live.limits && live.limits.cpuCores, formatCPU),
      progress: ratio(live.usage && live.usage.cpuCores, (live.limits && live.limits.cpuCores) || (live.requests && live.requests.cpuCores)),
      stats: cpuStats,
    }),
    kpiCard({
      label: "Memory",
      value: formatBytes(live.usage && live.usage.memoryBytes),
      meta: requestLimitMeta(live.requests && live.requests.memoryBytes, live.limits && live.limits.memoryBytes, formatBytes),
      progress: ratio(live.usage && live.usage.memoryBytes, (live.limits && live.limits.memoryBytes) || (live.requests && live.requests.memoryBytes)),
      stats: memoryStats,
    }),
    kpiCard({
      label: "Disk",
      value: storageUsed ? formatBytes(storageUsed.last) : "—",
      meta: storageCapacity ? `of ${formatBytes(storageCapacity.last)}` : "No volume metrics",
      progress: storageUsed && storageCapacity ? ratio(storageUsed.last, storageCapacity.last) : null,
      stats: storageUsed,
    }),
    kpiCard({
      label: "Network",
      value: networkRx && networkTx ? formatRate(networkRx.last + networkTx.last) : "—",
      meta: networkRx && networkTx
        ? `in ${formatRate(networkRx.last)} · out ${formatRate(networkTx.last)}`
        : "No network metrics",
      stats: networkRx && networkTx ? {
        last: networkRx.last + networkTx.last,
        avg: networkRx.avg + networkTx.avg,
        max: networkRx.max + networkTx.max,
        unit: "bytes/s",
      } : null,
    }),
    kpiCard({
      label: "Disk IOPS",
      value: fsReadIOPS && fsWriteIOPS ? `${Math.round(fsReadIOPS.last)} / ${Math.round(fsWriteIOPS.last)}` : "—",
      meta: "read / write",
      stats: fsReadIOPS && fsWriteIOPS ? {
        last: fsReadIOPS.last + fsWriteIOPS.last,
        avg: fsReadIOPS.avg + fsWriteIOPS.avg,
        max: fsReadIOPS.max + fsWriteIOPS.max,
        unit: "iops",
      } : null,
    }),
    kpiCard({
      label: "Health",
      value: oom ? "OOM" : ready ? "Ready" : (live.phase || "Unknown"),
      meta: `${restarts} restart${restarts === 1 ? "" : "s"}`,
      tone: oom || !ready ? "warning" : "success",
      stats: null,
    }),
  );
  body.append(kpis);

  if (!live.available || !history.available) {
    const notes = document.createElement("div");
    notes.className = "monitoring-notes";
    if (!live.available && monitoringLiveEnabled()) appendMonitoringNote(notes, live.reason || "Live metrics are unavailable.");
    if (!history.available && monitoringHistoryEnabled()) appendMonitoringNote(notes, history.reason || "Prometheus history is unavailable.");
    if (notes.children.length) body.append(notes);
  }

  if (monitoringHistoryEnabled() && history.available && Array.isArray(history.series) && history.series.length) {
    const heading = document.createElement("div");
    heading.className = "monitoring-section-heading";
    heading.innerHTML = `<div><h4>Trends</h4><p>${escapeText(monitoringState.detailRange)} window with averages and peaks.</p></div>`;
    const charts = document.createElement("section");
    charts.className = "monitoring-charts workspace-monitoring-charts";
    const order = ["cpu", "memory", "storageUsed", "storageCapacity", "fsRead", "fsWrite", "fsReadIOPS", "fsWriteIOPS", "networkRx", "networkTx", "networkErrors", "networkDrops", "cpuThrottle", "restarts"];
    const series = history.series.slice().sort((a, b) => order.indexOf(a.key) - order.indexOf(b.key));
    charts.append(...series.map((item) => chartCard(item)));
    body.append(heading, charts);
  }

  if (Array.isArray(live.containers) && live.containers.length) {
    body.append(renderContainerTable(live.containers));
  } else if (!live.available && !history.available) {
    appendMonitoringNote(body, "No monitoring data is available for this workspace.");
  }

  if (els.detailMonitoringNote) {
    els.detailMonitoringNote.textContent = `${workspace.name} · ${monitoringState.detailRange} window`;
  }
}

function renderContainerTable(containers) {
  const section = document.createElement("section");
  section.className = "monitoring-containers";
  const header = document.createElement("div");
  header.className = "monitoring-section-heading";
  header.innerHTML = "<div><h4>Containers</h4><p>Live usage and declared resources.</p></div>";
  section.append(header);

  const wrap = document.createElement("div");
  wrap.className = "monitoring-table-wrap";
  const table = document.createElement("table");
  table.className = "workspace-table monitoring-table compact";
  const thead = document.createElement("thead");
  const headRow = document.createElement("tr");
  for (const label of ["Container", "State", "CPU", "Memory", "Requests", "Limits", "Restarts"]) {
    const th = document.createElement("th");
    th.textContent = label;
    headRow.append(th);
  }
  thead.append(headRow);
  const tbody = document.createElement("tbody");
  for (const container of containers) {
    const tr = document.createElement("tr");
    tr.append(textCell(container.name, "monitoring-container-name"));
    tr.append(textCell(container.reason || container.state || "unknown", container.oomKilled ? "monitoring-danger" : "monitoring-muted"));
    tr.append(textCell(formatCPU(container.usage && container.usage.cpuCores)));
    tr.append(textCell(formatBytes(container.usage && container.usage.memoryBytes)));
    tr.append(textCell(`${formatCPU(container.requests && container.requests.cpuCores)} · ${formatBytes(container.requests && container.requests.memoryBytes)}`, "monitoring-muted"));
    tr.append(textCell(`${formatCPU(container.limits && container.limits.cpuCores)} · ${formatBytes(container.limits && container.limits.memoryBytes)}`, "monitoring-muted"));
    tr.append(textCell(String(container.restarts || 0), container.restarts ? "monitoring-warning" : "monitoring-muted"));
    tbody.append(tr);
  }
  table.append(thead, tbody);
  wrap.append(table);
  section.append(wrap);
  return section;
}

function startDetailMonitoringTimer() {
  if (monitoringState.detailTimer) return;
  monitoringState.detailTimer = setInterval(() => {
    if (document.hidden || !els.workspaceDetailView || els.workspaceDetailView.hidden) return;
    if (!state.detailWorkspace) return;
    loadWorkspaceMonitoring(state.detailWorkspace, { silent: true });
  }, 15000);
}

function stopWorkspaceMonitoring() {
  clearInterval(monitoringState.detailTimer);
  monitoringState.detailTimer = 0;
  monitoringState.detailRequest += 1;
}

function kpiCard({ label, value, meta = "", progress = null, tone = "", stats = null }) {
  const card = document.createElement("article");
  card.className = `monitoring-kpi ${tone ? `tone-${tone}` : ""}`.trim();

  const top = document.createElement("div");
  top.className = "monitoring-kpi-top";
  const title = document.createElement("span");
  title.textContent = label;
  top.append(title);
  card.append(top);

  const number = document.createElement("strong");
  number.className = "monitoring-kpi-value";
  number.textContent = value || "—";
  card.append(number);

  if (meta) {
    const detail = document.createElement("span");
    detail.className = "monitoring-kpi-meta";
    detail.textContent = meta;
    card.append(detail);
  }

  if (progress != null && Number.isFinite(progress)) {
    const bar = document.createElement("div");
    bar.className = "monitoring-bar";
    const fill = document.createElement("span");
    fill.style.width = `${Math.max(2, Math.min(100, progress * 100))}%`;
    if (progress >= 0.9) fill.className = "danger";
    else if (progress >= 0.7) fill.className = "warning";
    bar.append(fill);
    card.append(bar);
  }

  if (stats) {
    const statLine = document.createElement("span");
    statLine.className = "monitoring-kpi-stats";
    statLine.textContent = `avg ${formatMetric(stats.avg, stats.unit)} · peak ${formatMetric(stats.max, stats.unit)}`;
    card.append(statLine);
  }
  return card;
}

function chartCard(series) {
  const card = document.createElement("article");
  card.className = `monitoring-chart-card tone-${series.key || "default"}`;
  const stats = seriesStats({ available: true, series: [series] }, series.key);
  const header = document.createElement("header");
  const title = document.createElement("div");
  title.className = "monitoring-chart-title";
  const label = document.createElement("span");
  label.textContent = series.label || series.key;
  const value = document.createElement("strong");
  value.textContent = formatMetric(stats ? stats.last : null, series.unit);
  title.append(label, value);

  const meta = document.createElement("span");
  meta.className = "monitoring-chart-meta";
  meta.textContent = stats
    ? `avg ${formatMetric(stats.avg, series.unit)} · peak ${formatMetric(stats.max, series.unit)}`
    : "";
  header.append(title, meta);
  card.append(header, chartSvg(series));
  return card;
}

function chartSvg(series) {
  const samples = Array.isArray(series.points) ? downsample(series.points, 180) : [];
  const points = samples.filter((point) => Number.isFinite(Number(point.Value ?? point.v)));
  if (!points.length) {
    const empty = document.createElement("p");
    empty.className = "monitoring-note";
    empty.textContent = "No samples.";
    return empty;
  }
  const values = points.map((point) => Number(point.Value ?? point.v));
  let min = Math.min(...values);
  let max = Math.max(...values);
  if (!Number.isFinite(min) || !Number.isFinite(max)) {
    min = 0;
    max = 1;
  }
  if (min === max) {
    const padding = Math.abs(max) * 0.08 || 1;
    min = Math.max(0, min - padding);
    max += padding;
  }
  const span = max - min || 1;
  const coords = points.map((point, index) => {
    const x = points.length === 1 ? 0 : (index / (points.length - 1)) * 100;
    const value = Number(point.Value ?? point.v);
    const y = 8 + (1 - (value - min) / span) * 84;
    return [x, y];
  });
  const line = coords.map(([x, y], index) => `${index === 0 ? "M" : "L"}${x.toFixed(2)},${y.toFixed(2)}`).join(" ");
  const area = `${line} L100,100 L0,100 Z`;
  const gradient = `monitoring-area-${++chartSequence}`;
  const last = coords[coords.length - 1];
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("class", "metric-chart");
  svg.setAttribute("viewBox", "0 0 100 100");
  svg.setAttribute("preserveAspectRatio", "none");
  svg.setAttribute("role", "img");
  svg.setAttribute("aria-label", `${series.label || series.key} history`);
  svg.innerHTML = `
    <defs>
      <linearGradient id="${gradient}" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0%" stop-color="currentColor" stop-opacity=".30"></stop>
        <stop offset="100%" stop-color="currentColor" stop-opacity=".02"></stop>
      </linearGradient>
    </defs>
    <line class="metric-grid" x1="0" y1="25" x2="100" y2="25"></line>
    <line class="metric-grid" x1="0" y1="50" x2="100" y2="50"></line>
    <line class="metric-grid" x1="0" y1="75" x2="100" y2="75"></line>
    <path class="metric-area" d="${area}" fill="url(#${gradient})"></path>
    <path class="metric-line" d="${line}"></path>
    <circle class="metric-point" cx="${last[0].toFixed(2)}" cy="${last[1].toFixed(2)}" r="1.7"></circle>
  `;
  return svg;
}

function seriesStats(history, key) {
  if (!history || !Array.isArray(history.series)) return null;
  const series = history.series.find((item) => item.key === key);
  if (!series || !Array.isArray(series.points) || !series.points.length) return null;
  const values = series.points.map((point) => Number(point.Value ?? point.v)).filter(Number.isFinite);
  if (!values.length) return null;
  const sum = values.reduce((total, value) => total + value, 0);
  return {
    last: values[values.length - 1],
    avg: sum / values.length,
    max: Math.max(...values),
    min: Math.min(...values),
    unit: series.unit || "",
  };
}

function downsample(points, limit) {
  if (points.length <= limit) return points;
  const stride = points.length / limit;
  const sampled = [];
  for (let i = 0; i < limit; i += 1) {
    const index = Math.min(points.length - 1, Math.floor(i * stride));
    const point = points[index];
    sampled.push({
      Timestamp: Number(point.Timestamp ?? point.t),
      Value: Number(point.Value ?? point.v),
    });
  }
  return sampled;
}

function ratio(used, total) {
  if (used == null || total == null || !Number.isFinite(Number(used)) || !Number.isFinite(Number(total)) || Number(total) <= 0) {
    return null;
  }
  return Number(used) / Number(total);
}

function requestLimitMeta(request, limit, formatter) {
  if (request == null && limit == null) return "No declared resources";
  const bits = [];
  if (request != null) bits.push(`req ${formatter(request)}`);
  if (limit != null) bits.push(`lim ${formatter(limit)}`);
  return bits.join(" · ");
}

function formatMetric(value, unit) {
  if (value == null || !Number.isFinite(Number(value))) return "—";
  switch (unit) {
    case "cores":
      return formatCPU(value);
    case "bytes":
      return formatBytes(value);
    case "bytes/s":
      return `${formatBytes(value)}/s`;
    case "ratio":
      return `${(Number(value) * 100).toFixed(1)}%`;
    case "iops":
      return `${Math.round(Number(value))} IOPS`;
    case "pps":
      return `${Math.round(Number(value))}/s`;
    case "count":
      return String(Math.round(Number(value)));
    default:
      return String(value);
  }
}

function formatCPU(value) {
  if (value == null || !Number.isFinite(Number(value))) return "—";
  const cores = Number(value);
  if (cores === 0) return "0";
  if (Math.abs(cores) < 1) return `${Math.round(cores * 1000)}m`;
  return `${cores.toFixed(cores < 10 ? 2 : 1)} cores`;
}

function formatBytes(value) {
  if (value == null || !Number.isFinite(Number(value))) return "—";
  let bytes = Number(value);
  if (bytes === 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  let index = 0;
  while (Math.abs(bytes) >= 1024 && index < units.length - 1) {
    bytes /= 1024;
    index += 1;
  }
  const digits = bytes >= 100 ? 0 : bytes >= 10 ? 1 : 2;
  return `${bytes.toFixed(digits)} ${units[index]}`;
}

function formatRate(value) {
  return `${formatBytes(value)}/s`;
}

function statusBadge(phase) {
  const badge = document.createElement("span");
  badge.className = "status";
  const dot = document.createElement("span");
  dot.className = `dot ${typeof statusClass === "function" ? statusClass(phase) : ""}`.trim();
  badge.append(dot, document.createTextNode(phase || "Unknown"));
  return badge;
}

function usageCell(used, request, formatter, kind) {
  const cell = document.createElement("td");
  cell.className = "monitoring-usage-cell";
  const line = document.createElement("span");
  line.className = "monitoring-usage-line";
  line.textContent = formatter(used);
  const bar = document.createElement("span");
  bar.className = "monitoring-mini-bar";
  const fill = document.createElement("span");
  fill.className = kind === "memory" ? "memory" : "cpu";
  const value = ratio(used, request);
  fill.style.width = value == null ? "0%" : `${Math.max(2, Math.min(100, value * 100))}%`;
  if (value != null && value >= 0.9) fill.classList.add("danger");
  bar.append(fill);
  cell.append(line, bar);
  return cell;
}

function textCell(value, className = "") {
  const cell = document.createElement("td");
  if (className) cell.className = className;
  cell.textContent = value;
  return cell;
}

function appendMonitoringNote(container, message) {
  const note = document.createElement("p");
  note.className = "monitoring-note";
  note.textContent = message;
  container.append(note);
}

function escapeText(value) {
  return String(value).replace(/[&<>"']/g, (char) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  }[char]));
}

if (els.manageMonitoring) els.manageMonitoring.addEventListener("click", openMonitoring);
if (els.monitoringBack) els.monitoringBack.addEventListener("click", closeMonitoring);
if (els.monitoringRefresh) els.monitoringRefresh.addEventListener("click", () => loadMonitoring());
if (els.monitoringRange) {
  els.monitoringRange.addEventListener("click", (event) => {
    const button = event.target.closest("[data-monitoring-range]");
    if (!button) return;
    monitoringState.range = button.dataset.monitoringRange;
    for (const item of els.monitoringRange.querySelectorAll("[data-monitoring-range]")) {
      const active = item === button;
      item.classList.toggle("active", active);
      item.setAttribute("aria-pressed", String(active));
    }
    loadMonitoring();
  });
}
if (els.detailMonitoringRefresh) {
  els.detailMonitoringRefresh.addEventListener("click", () => {
    if (state.detailWorkspace) loadWorkspaceMonitoring(state.detailWorkspace);
  });
}
if (els.detailMonitoringRange) {
  els.detailMonitoringRange.addEventListener("change", () => {
    monitoringState.detailRange = els.detailMonitoringRange.value || "1h";
    if (state.detailWorkspace) loadWorkspaceMonitoring(state.detailWorkspace);
  });
}
