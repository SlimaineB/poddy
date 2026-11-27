"use strict";

const QUOTA_DEFAULT_UNIT = { cpu: "cores", memory: "Mi", storage: "Gi" };

function quotaInputs(scope) {
  if (scope === "team") {
    return {
      workspaces: els.teamQuotaWorkspaces,
      backups: els.teamQuotaBackups,
      cpu: els.teamQuotaCPU,
      memory: els.teamQuotaMemory,
      storage: els.teamQuotaStorage,
    };
  }
  return {
    workspaces: els.quotaWorkspaces,
    backups: els.quotaBackups,
    cpu: els.quotaCPU,
    memory: els.quotaMemory,
    storage: els.quotaStorage,
  };
}

function quotaUnitInputs(scope) {
  if (scope === "team") {
    return {
      cpu: els.teamQuotaCPUUnit,
      memory: els.teamQuotaMemoryUnit,
      storage: els.teamQuotaStorageUnit,
    };
  }
  return {
    cpu: els.quotaCPUUnit,
    memory: els.quotaMemoryUnit,
    storage: els.quotaStorageUnit,
  };
}

function splitQuotaValue(raw, kind) {
  const text = String(raw ?? "").trim();
  const fallback = QUOTA_DEFAULT_UNIT[kind] || "";
  if (!text) return { value: "", unit: fallback };
  const match = text.match(/^([+-]?(?:\d+\.?\d*|\.\d+))([a-zA-Z]*)$/);
  if (!match) return { value: text, unit: fallback };
  if (kind === "cpu") {
    return match[2] === "m" ? { value: match[1], unit: "m" } : { value: match[1], unit: "cores" };
  }
  return { value: match[1], unit: match[2] || fallback };
}

function combineQuotaValue(value, unit, kind) {
  const text = String(value ?? "").trim();
  if (!text) return "";
  if (kind === "cpu") return unit === "m" ? `${text}m` : text;
  return `${text}${unit || QUOTA_DEFAULT_UNIT[kind] || ""}`;
}

function readQuota(scope) {
  const inputs = quotaInputs(scope);
  const units = quotaUnitInputs(scope);
  const quota = {};
  for (const [key, label] of [["workspaces", "Max workspaces"], ["backups", "Max backups"]]) {
    const text = inputs[key].value.trim();
    if (!text) continue;
    const value = Number(text);
    if (!Number.isInteger(value) || value < 0) {
      throw new Error(`${label} must be a non-negative whole number.`);
    }
    if (key === "workspaces") quota.maxWorkspaces = value;
    else quota.maxBackups = value;
  }
  for (const key of ["cpu", "memory", "storage"]) {
    const value = combineQuotaValue(inputs[key].value, units[key]?.value, key);
    if (value) quota[key] = value;
  }
  return quota;
}

function setQuotaInputs(scope, quota) {
  const inputs = quotaInputs(scope);
  const units = quotaUnitInputs(scope);
  inputs.workspaces.value = quota?.maxWorkspaces ?? "";
  inputs.backups.value = quota?.maxBackups ?? "";
  for (const key of ["cpu", "memory", "storage"]) {
    const parts = splitQuotaValue(quota?.[key] || "", key);
    inputs[key].value = parts.value;
    if (units[key]) units[key].value = parts.unit;
  }
  if (scope === "team") renderTeamQuotaPreview();
}

function setQuotaDisabled(scope, disabled) {
  for (const input of Object.values(quotaInputs(scope))) input.disabled = Boolean(disabled);
  for (const unit of Object.values(quotaUnitInputs(scope))) unit.disabled = Boolean(disabled);
}

function quotaSummary(quota) {
  if (!quota) return "";
  const parts = [];
  if (quota.maxWorkspaces !== undefined && quota.maxWorkspaces !== null) {
    parts.push(`${quota.maxWorkspaces} workspace${quota.maxWorkspaces === 1 ? "" : "s"}`);
  }
  if (quota.maxBackups !== undefined && quota.maxBackups !== null) {
    parts.push(`${quota.maxBackups} backup${quota.maxBackups === 1 ? "" : "s"}/workspace`);
  }
  if (quota.cpu) parts.push(`${quota.cpu} CPU`);
  if (quota.memory) parts.push(`${quota.memory} memory`);
  if (quota.storage) parts.push(`${quota.storage} storage`);
  return parts.join(" · ");
}

function hasQuotaValues(scope) {
  return Object.values(quotaInputs(scope)).some((input) => input.value.trim() !== "");
}

function previewQuota(scope) {
  const inputs = quotaInputs(scope);
  const units = quotaUnitInputs(scope);
  const quota = {};
  let invalid = false;
  for (const [key, target] of [["workspaces", "maxWorkspaces"], ["backups", "maxBackups"]]) {
    const text = inputs[key].value.trim();
    if (!text) continue;
    const value = Number(text);
    if (Number.isInteger(value) && value >= 0) quota[target] = value;
    else invalid = true;
  }
  for (const key of ["cpu", "memory", "storage"]) {
    const value = combineQuotaValue(inputs[key].value, units[key]?.value, key);
    if (value) quota[key] = value;
  }
  return { quota, invalid };
}

function quotaChip(label, value) {
  const chip = document.createElement("span");
  chip.className = "quota-chip";
  const labelNode = document.createElement("span");
  labelNode.className = "quota-chip-label";
  labelNode.textContent = label;
  const valueNode = document.createElement("strong");
  valueNode.textContent = value;
  chip.append(labelNode, valueNode);
  return chip;
}

function quotaChips(quota, emptyLabel = "Unlimited") {
  const wrap = document.createElement("div");
  wrap.className = "quota-chips";
  const values = [];
  if (quota?.maxWorkspaces !== undefined && quota?.maxWorkspaces !== null) {
    values.push(["Workspaces", String(quota.maxWorkspaces)]);
  }
  if (quota?.maxBackups !== undefined && quota?.maxBackups !== null) {
    values.push(["Backups", String(quota.maxBackups)]);
  }
  if (quota?.cpu) values.push(["CPU", String(quota.cpu)]);
  if (quota?.memory) values.push(["Memory", String(quota.memory)]);
  if (quota?.storage) values.push(["Storage", String(quota.storage)]);
  if (!values.length) {
    const empty = document.createElement("span");
    empty.className = "quota-chip empty";
    empty.textContent = emptyLabel;
    wrap.append(empty);
    return wrap;
  }
  for (const [label, value] of values) wrap.append(quotaChip(label, value));
  return wrap;
}

function parseQuotaQuantity(value, kind) {
  const text = String(value ?? "").trim();
  if (!text) return null;
  if (kind === "workspaces") {
    const number = Number(text);
    return Number.isFinite(number) ? number : null;
  }
  const match = text.match(/^([+-]?(?:\d+\.?\d*|\.\d+))([a-zA-Z]*)$/);
  if (!match) return null;
  const amount = Number(match[1]);
  if (!Number.isFinite(amount)) return null;
  const binary = { Ki: 1024, Mi: 1024 ** 2, Gi: 1024 ** 3, Ti: 1024 ** 4, Pi: 1024 ** 5, Ei: 1024 ** 6 };
  const decimal = { "": 1, n: 1e-9, u: 1e-6, m: 1e-3, k: 1e3, K: 1e3, M: 1e6, G: 1e9, T: 1e12, P: 1e15, E: 1e18 };
  const suffix = match[2];
  let factor;
  if (suffix in binary) factor = binary[suffix];
  else if (suffix in decimal) factor = decimal[suffix];
  else factor = undefined;
  if (suffix === "") {
    if (kind === "memory") factor = binary.Mi;
    else if (kind === "storage") factor = binary.Gi;
    else factor = 1;
  }
  return factor === undefined ? null : amount * factor;
}

function quotaMeterCard(label, used, limit, kind) {
  const card = document.createElement("div");
  card.className = "quota-meter";
  const usedText = String(used ?? "").trim() || "0";
  const hasLimit = limit !== undefined && limit !== null && String(limit).trim() !== "";
  const limitText = hasLimit ? String(limit) : "Unlimited";
  const usedValue = parseQuotaQuantity(usedText, kind);
  const limitValue = hasLimit ? parseQuotaQuantity(limitText, kind) : null;
  let percent = null;
  if (limitValue !== null && usedValue !== null) {
    if (limitValue > 0) percent = Math.max(0, Math.round((usedValue / limitValue) * 100));
    else percent = usedValue > 0 ? 100 : 0;
  }

  const head = document.createElement("div");
  head.className = "quota-meter-head";
  const title = document.createElement("span");
  title.textContent = label;
  const status = document.createElement("span");
  status.className = "quota-meter-percent";
  status.textContent = hasLimit ? (percent === null ? "—" : `${percent}%`) : "No limit";
  head.append(title, status);

  const track = document.createElement("div");
  track.className = "quota-meter-track";
  const fill = document.createElement("span");
  if (hasLimit) {
    fill.style.width = `${Math.min(100, percent ?? 0)}%`;
    if (percent !== null && percent >= 100) card.classList.add("danger");
    else if (percent !== null && percent >= 80) card.classList.add("warning");
  } else {
    track.classList.add("unlimited");
    fill.style.width = "0%";
  }
  track.append(fill);

  const foot = document.createElement("div");
  foot.className = "quota-meter-foot";
  const usedNode = document.createElement("span");
  usedNode.textContent = `${usedText} used`;
  const limitNode = document.createElement("span");
  limitNode.textContent = hasLimit ? `${limitText} limit` : "No limit";
  foot.append(usedNode, limitNode);

  card.append(head, track, foot);
  return card;
}

function renderUserQuotaMeters() {
  if (!els.quotaMeters) return;
  const details = state.quotaDetails;
  if (!details) {
    els.quotaMeters.replaceChildren();
    return;
  }
  const override = previewQuota("user").quota;
  const effective = { ...(details.effective || {}) };
  for (const key of ["maxWorkspaces", "maxBackups", "cpu", "memory", "storage"]) {
    if (override[key] !== undefined) effective[key] = override[key];
  }
  const usage = details.usage || {};
  const metrics = [
    ["Workspaces", usage.workspaces ?? 0, effective.maxWorkspaces, "workspaces"],
    ["CPU", usage.cpu || "0", effective.cpu, "cpu"],
    ["Memory", usage.memory || "0", effective.memory, "memory"],
    ["Storage", usage.storage || "0", effective.storage, "storage"],
  ];
  els.quotaMeters.replaceChildren(...metrics.map(([label, used, limit, kind]) => quotaMeterCard(label, used, limit, kind)));
}

function updateUserQuotaMode() {
  if (!els.quotaMode) return;
  const override = hasQuotaValues("user");
  els.quotaMode.textContent = override ? "Override" : "Inherited";
  els.quotaMode.className = `badge ${override ? "quota-override" : "neutral"}`;
}

function updateUserQuotaPreview() {
  updateUserQuotaMode();
  renderUserQuotaMeters();
}

function renderTeamQuotaPreview() {
  if (!els.teamQuotaPreview) return;
  const preview = previewQuota("team");
  els.teamQuotaPreview.replaceChildren();
  if (preview.invalid) {
    const invalid = document.createElement("span");
    invalid.className = "quota-chip invalid";
    invalid.textContent = "Counts must be whole numbers";
    els.teamQuotaPreview.append(invalid);
    return;
  }
  els.teamQuotaPreview.append(quotaChips(preview.quota, "No limits"));
}

function quotaIdentityUser(value) {
  return String(value || "").trim().replace(/^(user|email):/i, "").toLowerCase();
}

function userQuotaEntry(user) {
  const target = quotaIdentityUser(user);
  if (!target) return null;
  return state.userQuotas.find((entry) => quotaIdentityUser(entry.identity) === target) || null;
}

function inheritedQuotaTeams(teamNames) {
  const out = [];
  for (const name of teamNames || []) {
    const team = state.teams.find((entry) => entry.name === name);
    if (team && quotaSummary(team.quota)) out.push(name);
  }
  return out;
}

async function loadUserQuotas() {
  if (!state.me.isAdmin) {
    state.userQuotas = [];
    return;
  }
  try {
    const data = await api("/api/user-quotas");
    state.userQuotas = Array.isArray(data) ? data : [];
  } catch {
    state.userQuotas = [];
  }
}

function openUserQuota(user, entry) {
  if (!els.userDetailView) return;
  state.quotaUser = user;
  state.quotaIdentity = entry?.identity || user;
  state.quotaDetails = null;
  if (els.userDetailTitle) els.userDetailTitle.textContent = user;
  els.quotaIdentity.textContent = state.me.isAdmin
    ? "Quota override and Poddy team memberships."
    : "Poddy team memberships.";
  if (els.quotaAvatar) els.quotaAvatar.textContent = String(user).replace(/^(user|email):/i, "").trim().slice(0, 1).toUpperCase() || "?";
  if (els.quotaForm) els.quotaForm.hidden = !state.me.isAdmin;
  if (els.quotaMode) els.quotaMode.hidden = !state.me.isAdmin;
  renderUserDetailMeta(user, entry);
  setQuotaInputs("user", entry?.quota || null);
  updateUserQuotaMode();
  setFormError(els.quotaError, "");
  if (els.quotaStatus) els.quotaStatus.hidden = true;
  if (els.quotaMeters) els.quotaMeters.replaceChildren();
  if (els.quotaClear) els.quotaClear.hidden = !entry;
  showView("user-detail");
  if (!state.me.isAdmin) return;
  els.quotaWorkspaces.focus();
  loadUserQuotaDetails(state.quotaIdentity);
}

function usageSummary(usage) {
  if (!usage) return "";
  const parts = [`${usage.workspaces} workspace${usage.workspaces === 1 ? "" : "s"}`];
  for (const [key, label] of [["cpu", "CPU"], ["memory", "memory"], ["storage", "storage"]]) {
    const value = String(usage[key] || "").trim();
    if (value && value !== "0") parts.push(`${value} ${label}`);
  }
  return parts.join(" · ");
}

async function loadCreateQuota() {
  if (!els.createQuota) return;
  els.createQuota.hidden = true;
  try {
    const data = await api("/api/me/quota");
    const usage = usageSummary(data.usage) || "No workspaces";
    const limit = quotaSummary(data.effective);
    els.createQuota.textContent = limit
      ? `Current usage: ${usage}. Your quota: ${limit}.`
      : `Current usage: ${usage}. No quota limit.`;
    els.createQuota.hidden = false;
  } catch {
    if (els.createQuota) els.createQuota.hidden = true;
  }
}

async function loadUserQuotaDetails(identity) {
  if (!els.quotaStatus) return;
  try {
    const data = await api(`/api/user-quotas/${encodeURIComponent(identity)}`);
    if (state.quotaIdentity !== identity) return;
    state.quotaDetails = data;
    renderUserQuotaMeters();
    updateUserQuotaMode();
    els.quotaStatus.hidden = false;
  } catch {
    state.quotaDetails = null;
    if (els.quotaStatus) els.quotaStatus.hidden = true;
  }
}

async function clearUserQuota() {
  const user = state.quotaUser;
  const identity = state.quotaIdentity || user;
  if (!user || !identity || !els.quotaClear) return;

  const original = els.quotaClear.textContent;
  els.quotaClear.disabled = true;
  els.quotaClear.textContent = "Removing…";
  setFormError(els.quotaError, "");
  try {
    await api(`/api/user-quotas/${encodeURIComponent(identity)}`, { method: "DELETE" });
    toast(`Quota override for ${user} removed.`, "success");
    await loadUserQuotas();
    renderUsers();
    openUserQuota(user, userQuotaEntry(user));
  } catch (err) {
    setFormError(els.quotaError, err.message || "Failed to remove quota.");
  } finally {
    els.quotaClear.disabled = false;
    els.quotaClear.textContent = original;
  }
}

async function saveUserQuota(event) {
  event.preventDefault();
  const user = state.quotaUser;
  const identity = state.quotaIdentity || user;
  if (!user || !identity) return;

  let quota;
  try {
    quota = readQuota("user");
  } catch (err) {
    setFormError(els.quotaError, err.message);
    return;
  }

  const original = els.quotaSave.innerHTML;
  els.quotaSave.disabled = true;
  els.quotaSave.innerHTML = `<span class="spinner small"></span>Saving`;
  setFormError(els.quotaError, "");
  try {
    await api(`/api/user-quotas/${encodeURIComponent(identity)}`, {
      method: "PUT",
      body: JSON.stringify(quota),
    });
    toast(Object.keys(quota).length ? `Quota for ${user} saved.` : `Quota override for ${user} removed.`, "success");
    await loadUserQuotas();
    renderUsers();
    openUserQuota(user, userQuotaEntry(user));
  } catch (err) {
    setFormError(els.quotaError, err.message || "Failed to save quota.");
  } finally {
    els.quotaSave.disabled = false;
    els.quotaSave.innerHTML = original;
  }
}

function myQuotaPercent(used, limit, kind) {
  const usedValue = parseQuotaQuantity(used, kind);
  const limitValue = limit === undefined || limit === null || String(limit).trim() === "" ? null : parseQuotaQuantity(limit, kind);
  if (limitValue === null || usedValue === null) return null;
  if (limitValue <= 0) return usedValue > 0 ? 100 : 0;
  return Math.max(0, Math.round((usedValue / limitValue) * 100));
}

function renderMyQuotaButton() {
  if (!els.myQuota) return;
  const data = state.myQuota;
  if (!data) {
    els.myQuota.hidden = true;
    return;
  }
  const usage = data.usage || {};
  const effective = data.effective || {};
  const used = Number(usage.workspaces ?? 0);
  const hasWorkspaceLimit = effective.maxWorkspaces !== undefined && effective.maxWorkspaces !== null;
  els.myQuota.hidden = false;
  els.myQuotaLabel.textContent = hasWorkspaceLimit ? `Quota ${used}/${effective.maxWorkspaces}` : `Quota ${used}`;
  const percent = hasWorkspaceLimit ? myQuotaPercent(used, effective.maxWorkspaces, "workspaces") : null;
  els.myQuota.classList.toggle("warning", percent !== null && percent >= 80 && percent < 100);
  els.myQuota.classList.toggle("danger", percent !== null && percent >= 100);
  const limit = quotaSummary(effective);
  els.myQuota.title = limit ? `Your quota: ${limit}. Usage: ${usageSummary(usage)}.` : `No quota limit. Usage: ${usageSummary(usage)}.`;
}

function renderMyQuotaDialog() {
  if (!els.myQuotaMeters) return;
  const data = state.myQuota;
  if (!data) {
    els.myQuotaSummary.textContent = "Quota information is not available.";
    els.myQuotaMeters.replaceChildren();
    return;
  }
  const effective = data.effective || {};
  const usage = data.usage || {};
  const limit = quotaSummary(effective);
  els.myQuotaSummary.textContent = limit
    ? `You are using ${usageSummary(usage)}. Your limits: ${limit}.`
    : `You are using ${usageSummary(usage)}. No quota limits are set.`;
  const metrics = [
    ["Workspaces", usage.workspaces ?? 0, effective.maxWorkspaces, "workspaces"],
    ["CPU", usage.cpu || "0", effective.cpu, "cpu"],
    ["Memory", usage.memory || "0", effective.memory, "memory"],
    ["Storage", usage.storage || "0", effective.storage, "storage"],
  ];
  els.myQuotaMeters.replaceChildren(...metrics.map(([label, used, max, kind]) => quotaMeterCard(label, used, max, kind)));
}

async function loadMyQuota() {
  if (!els.myQuota) return;
  try {
    state.myQuota = await api("/api/me/quota");
    renderMyQuotaButton();
    renderMyQuotaDialog();
  } catch {
    state.myQuota = null;
    renderMyQuotaButton();
    renderMyQuotaDialog();
  }
}

async function openMyQuota() {
  if (!els.myQuotaDialog) return;
  renderMyQuotaDialog();
  els.myQuotaDialog.showModal();
  await loadMyQuota();
}

if (els.myQuota) els.myQuota.addEventListener("click", openMyQuota);
