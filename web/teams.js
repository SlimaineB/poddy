"use strict";

async function refreshTeams() {
  const teams = await api("/api/teams");
  state.teams = Array.isArray(teams) ? teams : [];
  renderTeams();
}
function renderTeams() {
  els.teamsCount.textContent = `${state.teams.length} team${state.teams.length === 1 ? "" : "s"}`;
  els.teamsList.replaceChildren();
  if (!state.teams.length) {
    els.teamsList.append(emptyInline("No teams yet. Create one with New team."));
    return;
  }
  for (const team of state.teams) {
    const item = document.createElement("div");
    item.className = "team-item";
    const info = document.createElement("div");
    const name = document.createElement("h3");
    name.textContent = team.name;
    const count = document.createElement("p");
    const members = team.memberCount || 0;
    count.textContent = `${members} member${members === 1 ? "" : "s"}`;
    info.append(name, count);
    const actions = document.createElement("div");
    actions.className = "team-actions";
    const edit = document.createElement("button");
    edit.type = "button";
    edit.className = "button small";
    edit.textContent = "Edit";
    edit.addEventListener("click", () => editTeam(team));
    const del = document.createElement("button");
    del.type = "button";
    del.className = "button danger small";
    del.textContent = "Delete";
    del.addEventListener("click", () => deleteTeam(team));
    actions.append(edit, del);
    item.append(info, actions);
    els.teamsList.append(item);
  }
}
async function openTeams() {
  try {
    await Promise.all([refreshTeams(), loadDirectory()]);
    showView("teams");
    newTeam();
  } catch (err) {
    toast(err.message || "Failed to load teams.");
  }
}
function editTeam(team) {
  state.editingTeam = team.name;
  els.teamFormTitle.textContent = `Edit ${team.name}`;
  els.teamName.value = team.name;
  els.teamName.readOnly = true;
  entryLists.teamMembers.setValues(team.members || []);
  els.teamSave.textContent = "Save team";
  els.teamForm.scrollIntoView({ behavior: "smooth", block: "start" });
  entryLists.teamMembers.focus();
}
function newTeam() {
  state.editingTeam = null;
  els.teamForm.reset();
  els.teamFormTitle.textContent = "New team";
  entryLists.teamMembers.setValues([]);
  els.teamName.readOnly = false;
  els.teamSave.textContent = "Create team";
  els.teamName.focus();
}
async function saveTeam(event) {
  event.preventDefault();
  const name = els.teamName.value.trim();
  if (!name) return;
  const members = entryLists.teamMembers.getValues();

  const original = els.teamSave.innerHTML;
  let saved = false;
  els.teamSave.disabled = true;
  els.teamSave.innerHTML = `<span class="spinner small"></span>Saving`;
  try {
    await api(`/api/teams/${encodeURIComponent(name)}`, {
      method: "PUT",
      body: JSON.stringify({ members }),
    });
    toast(`Team ${name} saved.`, "success");
    state.editingTeam = name;
    els.teamName.readOnly = true;
    els.teamFormTitle.textContent = `Edit ${name}`;
    els.teamSave.textContent = "Save team";
    saved = true;
    await refreshTeams();
    const current = state.teams.find((team) => team.name === name);
    entryLists.teamMembers.setValues(current?.members || members);
  } catch (err) {
    toast(err.message || "Failed to save team.");
  } finally {
    els.teamSave.disabled = false;
    if (!saved) els.teamSave.innerHTML = original;
  }
}
async function deleteTeam(team) {
  if (!window.confirm(`Delete team "${team.name}"? It will be removed from every template.`)) return;
  try {
    await api(`/api/teams/${encodeURIComponent(team.name)}`, { method: "DELETE" });
    toast(`Team ${team.name} deleted.`, "success");
    if (state.editingTeam === team.name) newTeam();
    await refreshTeams();
    await loadTemplates();
  } catch (err) {
    toast(err.message || "Failed to delete team.");
  }
}
function teamMemberships() {
  const users = new Map();
  const groups = new Map();
  for (const team of state.teams) {
    for (const entry of team.members || []) {
      const raw = String(entry || "").trim();
      if (!raw || raw === "*") continue;
      if (/^group:/i.test(raw)) {
        const group = directoryGroup(raw);
        if (!group) continue;
        if (!groups.has(group.toLowerCase())) groups.set(group.toLowerCase(), []);
        groups.get(group.toLowerCase()).push(team.name);
        continue;
      }
      const user = directoryUser(raw);
      if (!user) continue;
      if (!users.has(user.toLowerCase())) users.set(user.toLowerCase(), []);
      users.get(user.toLowerCase()).push(team.name);
    }
  }
  return { users, groups };
}
