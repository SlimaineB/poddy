"use strict";

function quotaInputs(scope) {
  if (scope === "team") {
    return {
      workspaces: els.teamQuotaWorkspaces,
      cpu: els.teamQuotaCPU,
      memory: els.teamQuotaMemory,
      storage: els.teamQuotaStorage,
    };
  }
  return {
    workspaces: els.quotaWorkspaces,
    cpu: els.quotaCPU,
    memory: els.quotaMemory,
    storage: els.quotaStorage,
  };
}

function readQuota(scope) {
  const inputs = quotaInputs(scope);
  const quota = {};
  const workspaces = inputs.workspaces.value.trim();
  if (workspaces) {
    const value = Number(workspaces);
    if (!Number.isInteger(value) || value < 0) {
      throw new Error("Max workspaces must be a non-negative whole number.");
    }
    quota.maxWorkspaces = value;
  }
  for (const key of ["cpu", "memory", "storage"]) {
    const value = inputs[key].value.trim();
    if (value) quota[key] = value;
  }
  return quota;
}

function setQuotaInputs(scope, quota) {
  const inputs = quotaInputs(scope);
  inputs.workspaces.value = quota?.maxWorkspaces ?? "";
  inputs.cpu.value = quota?.cpu || "";
  inputs.memory.value = quota?.memory || "";
  inputs.storage.value = quota?.storage || "";
}

function setQuotaDisabled(scope, disabled) {
  for (const input of Object.values(quotaInputs(scope))) input.disabled = Boolean(disabled);
}

function quotaSummary(quota) {
  if (!quota) return "";
  const parts = [];
  if (quota.maxWorkspaces !== undefined && quota.maxWorkspaces !== null) {
    parts.push(`${quota.maxWorkspaces} workspace${quota.maxWorkspaces === 1 ? "" : "s"}`);
  }
  if (quota.cpu) parts.push(`${quota.cpu} CPU`);
  if (quota.memory) parts.push(`${quota.memory} memory`);
  if (quota.storage) parts.push(`${quota.storage} storage`);
  return parts.join(" · ");
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
  if (!els.quotaDialog) return;
  state.quotaUser = user;
  state.quotaIdentity = entry?.identity || user;
  els.quotaIdentity.textContent = `Override for ${user}.`;
  setQuotaInputs("user", entry?.quota || null);
  setFormError(els.quotaError, "");
  if (els.quotaStatus) els.quotaStatus.hidden = true;
  if (els.quotaClear) els.quotaClear.hidden = !entry;
  els.quotaDialog.showModal();
  els.quotaWorkspaces.focus();
  loadUserQuotaDetails(state.quotaUser);
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

async function loadUserQuotaDetails(identity) {
  if (!els.quotaStatus) return;
  try {
    const data = await api(`/api/user-quotas/${encodeURIComponent(identity)}`);
    if (state.quotaUser !== identity) return;
    els.quotaEffective.textContent = quotaSummary(data.effective) || "Unlimited";
    els.quotaUsage.textContent = usageSummary(data.usage) || "No workspaces";
    els.quotaStatus.hidden = false;
  } catch {
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
    els.quotaDialog.close();
    await loadUserQuotas();
    renderUsers();
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
    els.quotaDialog.close();
    await loadUserQuotas();
    renderUsers();
  } catch (err) {
    setFormError(els.quotaError, err.message || "Failed to save quota.");
  } finally {
    els.quotaSave.disabled = false;
    els.quotaSave.innerHTML = original;
  }
}
