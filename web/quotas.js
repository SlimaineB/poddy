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
  els.quotaDialog.showModal();
  els.quotaWorkspaces.focus();
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
