"use strict";

const envState = {
  scope: "user",
  target: "",
  editing: "",
  items: [],
};

function envDefaultTarget(scope) {
  if (scope === "user") return state.me.email || state.me.username || "";
  if (scope === "team") return state.teams[0]?.name || "";
  if (scope === "template") return state.templates.find((item) => item.canManage)?.name || state.templates[0]?.name || "";
  if (scope === "workspace") return state.workspaces[0]?.name || "";
  return "";
}

function envTargetValues(scope) {
  if (scope === "team") return state.teams.map((team) => team.name);
  if (scope === "template") return state.templates.map((template) => template.name);
  if (scope === "workspace") return state.workspaces.map((workspace) => workspace.name);
  if (scope === "user") {
    const values = state.directory.users.slice();
    const own = state.me.email || state.me.username;
    if (own && !values.some((value) => value.toLowerCase() === own.toLowerCase())) values.unshift(own);
    return values;
  }
  return [];
}

function syncEnvironmentTargets() {
  const scope = envState.scope;
  els.envTargetList.replaceChildren();
  for (const value of envTargetValues(scope)) {
    const option = document.createElement("option");
    option.value = value;
    els.envTargetList.append(option);
  }
  els.envScopeHint.textContent = scope === "template"
    ? "Template defaults apply to every workspace using this template."
    : scope === "team"
      ? "Team values apply to members. User values override team values."
      : scope === "user"
        ? "User values override team and template values for this identity."
        : "Workspace values have the highest precedence for this workspace.";
  if (!envState.target) envState.target = envDefaultTarget(scope);
  els.envTarget.value = envState.target;
  els.envTarget.placeholder = scope === "user" ? "email or username" : `Select a ${scope}`;
}

function resetEnvironmentForm() {
  envState.editing = "";
  els.envName.value = "";
  els.envValue.value = "";
  els.envSecret.checked = false;
  els.envValue.type = "text";
  els.envSave.textContent = "Add variable";
  els.envCancel.hidden = true;
  els.envFormError.hidden = true;
}

function renderEnvironment() {
  els.envList.replaceChildren();
  const secretCount = envState.items.filter((item) => item.secret).length;
  els.envCount.textContent = `${envState.items.length} variable${envState.items.length === 1 ? "" : "s"}${secretCount ? ` · ${secretCount} secret${secretCount === 1 ? "" : "s"}` : ""}`;
  if (!envState.items.length) {
    els.envList.append(emptyInline("No variables at this scope. Add one on the left."));
    return;
  }
  for (const item of envState.items) {
    const row = document.createElement("div");
    row.className = "env-item";
    const main = document.createElement("div");
    main.className = "env-main";
    const name = document.createElement("code");
    name.className = "env-name";
    name.textContent = item.name;
    const value = document.createElement("span");
    value.className = "env-value";
    value.textContent = item.secret ? "•••••••• (secret)" : item.value || "(empty)";
    main.append(name, value);
    if (item.secret) {
      const badge = document.createElement("span");
      badge.className = "badge owner";
      badge.textContent = "secret";
      main.append(badge);
    }
    const actions = document.createElement("div");
    actions.className = "env-actions";
    const edit = document.createElement("button");
    edit.type = "button";
    edit.className = "button ghost small";
    edit.textContent = "Edit";
    edit.addEventListener("click", () => editEnvironment(item));
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "button danger small";
    remove.textContent = "Delete";
    remove.addEventListener("click", () => deleteEnvironment(item));
    actions.append(edit, remove);
    row.append(main, actions);
    els.envList.append(row);
  }
}

async function loadEnvironment() {
  if (!envState.target) {
    envState.items = [];
    renderEnvironment();
    return;
  }
  const params = new URLSearchParams({ scope: envState.scope, target: envState.target });
  try {
    const data = await api(`/api/environment?${params.toString()}`);
    envState.items = Array.isArray(data.items) ? data.items : [];
    renderEnvironment();
  } catch (err) {
    envState.items = [];
    renderEnvironment();
    toast(err.message || "Failed to load environment variables.");
  }
}

function editEnvironment(item) {
  envState.editing = item.name;
  els.envName.value = item.name;
  els.envName.readOnly = true;
  els.envValue.value = "";
  els.envValue.type = item.secret ? "password" : "text";
  els.envSecret.checked = item.secret;
  els.envSecret.disabled = true;
  els.envSave.textContent = "Save variable";
  els.envCancel.hidden = false;
  els.envFormError.hidden = true;
  els.envValue.placeholder = item.secret ? "Enter a new secret value" : "";
  els.envValue.focus();
}

async function saveEnvironment(event) {
  event.preventDefault();
  const name = els.envName.value.trim();
  if (!name) return;
  const secret = els.envSecret.checked;
  if (envState.editing && secret && !els.envValue.value) {
    els.envFormError.textContent = "Enter a new value to replace the secret.";
    els.envFormError.hidden = false;
    return;
  }
  const button = els.envSave;
  const original = button.innerHTML;
  button.disabled = true;
  button.innerHTML = `<span class="spinner small"></span>Saving`;
  try {
    await api("/api/environment", {
      method: "PUT",
      body: JSON.stringify({
        scope: envState.scope,
        target: envState.target,
        name,
        value: els.envValue.value,
        secret,
      }),
    });
    toast(`Environment variable ${name} saved.`, "success");
    resetEnvironmentForm();
    els.envName.readOnly = false;
    els.envSecret.disabled = false;
    await loadEnvironment();
  } catch (err) {
    els.envFormError.textContent = err.message || "Failed to save environment variable.";
    els.envFormError.hidden = false;
  } finally {
    button.disabled = false;
    if (envState.editing || button.textContent === "Saving") button.innerHTML = original;
  }
}

async function deleteEnvironment(item) {
  if (!window.confirm(`Delete environment variable ${item.name} from this ${envState.scope}?`)) return;
  const params = new URLSearchParams({ scope: envState.scope, target: envState.target, name: item.name });
  try {
    await api(`/api/environment?${params.toString()}`, { method: "DELETE" });
    toast(`Environment variable ${item.name} deleted.`, "success");
    if (envState.editing === item.name) resetEnvironmentForm();
    await loadEnvironment();
  } catch (err) {
    toast(err.message || "Failed to delete environment variable.");
  }
}

async function openEnvironment() {
  try {
    await Promise.all([
      state.templates.length ? Promise.resolve() : loadTemplates({ paginated: true, page: 1 }),
      state.teams.length ? Promise.resolve() : refreshTeams(),
      state.directory.users.length ? Promise.resolve() : loadDirectory(),
    ]);
    envState.scope = "user";
    envState.target = envDefaultTarget("user");
    els.envScope.value = envState.scope;
    resetEnvironmentForm();
    els.envName.readOnly = false;
    els.envSecret.disabled = false;
    syncEnvironmentTargets();
    renderEnvironment();
    showView("environment");
    await loadEnvironment();
  } catch (err) {
    toast(err.message || "Failed to load environment variables.");
  }
}

function setEnvironmentScope(scope) {
  envState.scope = scope;
  envState.target = envDefaultTarget(scope);
  resetEnvironmentForm();
  els.envName.readOnly = false;
  els.envSecret.disabled = false;
  syncEnvironmentTargets();
  loadEnvironment();
}
