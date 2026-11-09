"use strict";

async function refreshTeams({ paginated = false, page = 1 } = {}) {
  const data = await api(paginated ? pageURL("/api/teams", page, state.teamPerPage) : "/api/teams");
  if (Array.isArray(data)) {
    state.teams = data;
    state.teamTotal = data.length;
    state.teamPages = data.length ? 1 : 0;
    state.teamPage = 1;
  } else {
    state.teams = Array.isArray(data.items) ? data.items : [];
    state.teamTotal = Number(data.total) || 0;
    state.teamPages = Number(data.pages) || 0;
    state.teamPage = Number(data.page) || 1;
    state.teamPerPage = Number(data.perPage) || state.teamPerPage;
  }
  renderTeams();
}
function renderTeamsPagination() {
  renderPagination(els.teamsPagination, {
    page: state.teamPage,
    pages: state.teamPages,
    total: state.teamTotal,
    summary: `${state.teamTotal} team${state.teamTotal === 1 ? "" : "s"}`,
  }, (page) => {
    state.teamPage = page;
    refreshTeams({ paginated: true, page }).then(renderTeamsPagination);
  });
}
function renderTeams() {
  const total = state.teamPages ? state.teamTotal : state.teams.length;
  els.teamsCount.textContent = `${total} team${total === 1 ? "" : "s"}`;
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
    const quota = quotaSummary(team.quota);
    if (quota) {
      const quotaLine = document.createElement("p");
      quotaLine.className = "quota-summary";
      quotaLine.textContent = `Quota: ${quota}`;
      info.append(quotaLine);
    }
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
    await Promise.all([refreshTeams({ paginated: true, page: 1 }), loadDirectory()]);
    renderTeamsPagination();
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
  setQuotaInputs("team", team.quota || null);
  setTeamQuotaEnabled(state.me.isAdmin);
  els.teamSave.textContent = "Save team";
  els.teamForm.scrollIntoView({ behavior: "smooth", block: "start" });
  entryLists.teamMembers.focus();
}
function newTeam() {
  state.editingTeam = null;
  els.teamForm.reset();
  els.teamFormTitle.textContent = "New team";
  entryLists.teamMembers.setValues([]);
  setQuotaInputs("team", null);
  setTeamQuotaEnabled(state.me.isAdmin);
  els.teamName.readOnly = false;
  els.teamSave.textContent = "Create team";
  els.teamName.focus();
}
async function saveTeam(event) {
  event.preventDefault();
  const name = els.teamName.value.trim();
  if (!name) return;
  const members = entryLists.teamMembers.getValues();
  let quota;
  try {
    quota = readQuota("team");
  } catch (err) {
    toast(err.message || "Invalid quota.");
    return;
  }

  const original = els.teamSave.innerHTML;
  let saved = false;
  els.teamSave.disabled = true;
  els.teamSave.innerHTML = `<span class="spinner small"></span>Saving`;
  try {
    const body = { members };
    if (state.me.isAdmin) body.quota = quota;
    await api(`/api/teams/${encodeURIComponent(name)}`, {
      method: "PUT",
      body: JSON.stringify(body),
    });
    toast(`Team ${name} saved.`, "success");
    state.editingTeam = name;
    els.teamName.readOnly = true;
    els.teamFormTitle.textContent = `Edit ${name}`;
    els.teamSave.textContent = "Save team";
    saved = true;
    await refreshTeams({ paginated: true, page: state.teamPage });
    renderTeamsPagination();
    const current = state.teams.find((team) => team.name === name);
    entryLists.teamMembers.setValues(current?.members || members);
    setQuotaInputs("team", current?.quota || null);
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
    await refreshTeams({ paginated: true, page: state.teamPage });
    renderTeamsPagination();
    await loadTemplates();
  } catch (err) {
    toast(err.message || "Failed to delete team.");
  }
}
function setTeamQuotaEnabled(enabled) {
  setQuotaDisabled("team", !enabled);
  els.teamQuotaHint.textContent = enabled
    ? "Applies to team members unless a user override is set. Leave a field empty for unlimited."
    : "Only admins can change team quotas.";
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
