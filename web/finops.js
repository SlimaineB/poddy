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

function finopsScrollTo(element) {
  if (!element) return;
  element.scrollIntoView({ behavior: "smooth", block: "start" });
}

function finopsAlertAction(label, run) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "finops-alert-link";
  button.textContent = label;
  button.addEventListener("click", run);
  return button;
}

function finopsAlertDocLink(label, href) {
  const link = document.createElement("a");
  link.className = "finops-alert-link";
  link.href = href;
  link.target = "_blank";
  link.rel = "noopener";
  link.innerHTML = `${label}${icon("external")}`;
  return link;
}

function finopsAlertOpen(view) {
  if (view === "users" && typeof openUsers === "function") return () => openUsers();
  if (view === "templates" && typeof openTemplates === "function") return () => openTemplates();
  if (view === "monitoring" && typeof openMonitoring === "function") return () => openMonitoring();
  return () => showView(view);
}

const FINOPS_ALERT_META = {
  danger: { icon: "alert", badge: "High impact" },
  warning: { icon: "clock", badge: "Worth a look" },
  neutral: { icon: "info", badge: "Info" },
  ok: { icon: "check", badge: "Healthy" },
};

function renderFinOpsMessage(message) {
  const meta = FINOPS_ALERT_META[message.tone] || FINOPS_ALERT_META.neutral;
  const article = document.createElement("article");
  article.className = `finops-alert tone-${message.tone}`;

  const mark = document.createElement("span");
  mark.className = "finops-alert-icon";
  mark.innerHTML = icon(meta.icon);

  const main = document.createElement("div");
  main.className = "finops-alert-main";

  const head = document.createElement("div");
  head.className = "finops-alert-head";
  const title = document.createElement("strong");
  title.className = "finops-alert-title";
  title.textContent = message.title;
  const badge = document.createElement("span");
  badge.className = "finops-alert-badge";
  badge.textContent = meta.badge;
  head.append(title, badge);

  const text = document.createElement("p");
  text.className = "finops-alert-text";
  text.textContent = message.text;

  main.append(head, text);

  if (message.actions.length) {
    const actions = document.createElement("div");
    actions.className = "finops-alert-actions";
    for (const action of message.actions) actions.append(action);
    main.append(actions);
  }

  article.append(mark, main);
  return article;
}

function renderFinOpsMessages(report, currency) {
  els.finopsMessages.replaceChildren();
  const totals = report.totals || {};
  const messages = [];
  const add = (tone, title, text, actions) => messages.push({ tone, title, text, actions: actions || [] });
  const ranking = () => finopsScrollTo(els.finopsBars && els.finopsBars.closest(".panel"));
  const tables = () => finopsScrollTo(els.finopsListWrap);
  const openUsers = finopsAlertOpen("users");
  const openTemplates = finopsAlertOpen("templates");
  const openMonitoring = finopsAlertOpen("monitoring");
  const docs = "https://slimaineb.github.io/operations/#monitor";

  if (!report.available && report.reason) {
    add("neutral", "Live metrics are unavailable", report.reason, [
      finopsAlertAction("Refresh", () => loadFinOps()),
      finopsAlertDocLink("Monitoring docs", docs),
    ]);
  }
  if (report.costVisible) {
    if (totals.wastePercent >= 60) {
      add(
        "danger",
        `${totals.wastePercent.toFixed(0)}% of reserved capacity is idle`,
        `You reserve compute that no one uses. Lowering CPU and memory requests on the workspaces below would recover about ${formatMoney(totals.wastedHourly * 730, currency)} per month.`,
        [
          finopsAlertAction("Open waste ranking", ranking),
          finopsAlertAction("Adjust quotas", openUsers),
          finopsAlertAction("Edit templates", openTemplates),
        ],
      );
    } else if (totals.wastePercent >= 30) {
      add(
        "warning",
        `${totals.wastePercent.toFixed(0)}% of reserved compute is idle`,
        `A few request adjustments would recover ${formatMoney(totals.wastedHourly * 730, currency)} per month without changing how workspaces are used.`,
        [
          finopsAlertAction("Open waste ranking", ranking),
          finopsAlertAction("Edit templates", openTemplates),
        ],
      );
    }
  } else {
    if (totals.cpuIdleCores > 0 || totals.memoryIdleBytes > 0) {
      add(
        "warning",
        "Reserved resources are idling",
        `${formatCPU(totals.cpuIdleCores)} CPU and ${formatBytes(totals.memoryIdleBytes)} memory are requested but unused. Right-size the requests in your templates to stop reserving them.`,
        [
          finopsAlertAction("Open waste ranking", ranking),
          finopsAlertAction("Edit templates", openTemplates),
        ],
      );
    }
    add(
      "neutral",
      "Cost estimates are turned off",
      "Waste is measured in CPU and memory only. Add hourly prices and enable monitoring.costs to turn idle reservations into estimated spend.",
      [
        finopsAlertDocLink("How cost tracking works", docs),
        finopsAlertAction("Open monitoring", openMonitoring),
      ],
    );
  }
  if (totals.oversized) {
    add(
      "warning",
      `${totals.oversized} workspace${totals.oversized === 1 ? " is" : "s are"} oversized`,
      "Requests are far above real usage, so the scheduler reserves capacity that never gets used. Compare each workspace with its peak usage before lowering its template.",
      [
        finopsAlertAction("Open waste ranking", ranking),
        finopsAlertAction("Edit templates", openTemplates),
      ],
    );
  }
  if (totals.missingRequests) {
    add(
      "neutral",
      `${totals.missingRequests} workspace${totals.missingRequests === 1 ? " has" : "s have"} no CPU or memory requests`,
      "Without requests, usage cannot be attributed or costed and the workspace is treated as best-effort. Set requests in the template or workspace parameters.",
      [
        finopsAlertAction("Edit templates", openTemplates),
        finopsAlertAction("Open workspaces", tables),
      ],
    );
  }
  if (totals.restarts) {
    add(
      "warning",
      `${totals.restarts} container restart${totals.restarts === 1 ? "" : "s"} detected`,
      "OOM kills and crash loops usually mean the memory limit is too low or the image is unstable. Check the restart reason before raising limits.",
      [
        finopsAlertAction("Open monitoring", openMonitoring),
        finopsAlertAction("Review workspaces", finopsAlertOpen("workspaces")),
      ],
    );
  }

  if (!messages.length) {
    add(
      "ok",
      "Usage matches your reservations",
      "Nothing to fix right now. Keep requests close to real usage as workloads change.",
      [],
    );
  }
  for (const message of messages) els.finopsMessages.append(renderFinOpsMessage(message));
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
  row.className = "finops-gauge";

  const donut = document.createElement("div");
  donut.className = `finops-donut tone-${tone}`;
  donut.style.setProperty("--gauge", percent == null ? 0 : percent);
  const donutContent = document.createElement("div");
  donutContent.className = "finops-donut-content";
  const percentValue = document.createElement("strong");
  percentValue.textContent = percent == null ? "—" : `${Math.round(percent)}%`;
  const percentLabel = document.createElement("span");
  percentLabel.textContent = "used";
  donutContent.append(percentValue, percentLabel);
  donut.append(donutContent);

  const info = document.createElement("div");
  info.className = "finops-gauge-info";
  const head = document.createElement("div");
  head.className = "finops-gauge-head";
  const name = document.createElement("span");
  name.className = "finops-gauge-label";
  name.textContent = label;
  const capacity = document.createElement("span");
  capacity.className = "finops-gauge-capacity";
  capacity.textContent = reserved > 0 ? `${formatter(reserved)} reserved` : "No reservation";
  head.append(name, capacity);

  const legend = document.createElement("div");
  legend.className = "finops-gauge-legend";
  const legendRow = (dotClass, legendLabel, value) => {
    const line = document.createElement("div");
    const key = document.createElement("span");
    const dot = document.createElement("span");
    dot.className = `finops-gauge-dot ${dotClass}`;
    key.append(dot, document.createTextNode(legendLabel));
    const amount = document.createElement("span");
    amount.textContent = value;
    line.append(key, amount);
    return line;
  };
  legend.append(
    legendRow("used", "Used", formatter(used)),
    legendRow("idle", "Idle", percent == null ? "—" : formatter(idle)),
  );
  info.append(head, legend);
  if (percent == null) {
    const hint = document.createElement("p");
    hint.className = "finops-gauge-hint";
    hint.textContent = "Add requests to measure waste.";
    info.append(hint);
  }

  row.append(donut, info);
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
    ? "Each gauge compares live usage with the capacity reserved by workspace requests. The idle share is capacity you pay for but do not use."
    : "Each gauge compares live usage with the capacity reserved by workspace requests. The idle share is capacity blocked from other workspaces, even when no prices are configured.";

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
