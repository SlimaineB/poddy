"use strict";

async function refreshTeams({ paginated = false, page = 1, summary = false } = {}) {
  let path = paginated ? pageURL("/api/teams", page, state.teamPerPage, state.teamQuery.trim()) : "/api/teams";
  if (summary) path += `${path.includes("?") ? "&" : "?"}summary=1`;
  const data = await api(path);
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
    refreshTeams({ paginated: true, page, summary: true }).then(renderTeamsPagination);
  });
}
function memberInitials(entry) {
  const raw = String(entry || "").trim();
  if (raw.toLowerCase().startsWith("group:")) return "#";
  const clean = raw.replace(/^(user|email):/i, "").trim();
  if (!clean) return "?";
  const parts = clean.split(/[\s._@-]+/).filter(Boolean);
  if (parts.length >= 2) return `${parts[0][0]}${parts[1][0]}`.toUpperCase();
  return clean.slice(0, 2).toUpperCase();
}

function splitTeamMembers(members) {
  const users = [];
  const groups = [];
  for (const entry of members || []) {
    const raw = String(entry || "").trim();
    if (!raw) continue;
    if (/^group:/i.test(raw)) {
      const group = directoryGroup(raw);
      if (group) groups.push(group);
    } else {
      users.push(raw);
    }
  }
  return { users, groups };
}

function combineTeamMembers(users, groups) {
  const out = [];
  const seen = new Set();
  const push = (value) => {
    const trimmed = String(value || "").trim();
    if (!trimmed) return;
    const key = trimmed.toLowerCase();
    if (seen.has(key)) return;
    seen.add(key);
    out.push(trimmed);
  };
  for (const user of users || []) push(user);
  for (const group of groups || []) {
    const name = directoryGroup(group);
    if (name) push(`group:${name}`);
  }
  return out;
}

function renderTeams() {
  const total = state.teamPages ? state.teamTotal : state.teams.length;
  els.teamsCount.textContent = `${total} team${total === 1 ? "" : "s"}`;
  els.teamsList.replaceChildren();
  if (!state.teams.length) {
    const row = document.createElement("tr");
    const cell = document.createElement("td");
    cell.colSpan = 5;
    cell.append(emptyInline(state.teamQuery.trim() ? "No teams match your search." : "No teams yet. Create one with New team."));
    row.append(cell);
    els.teamsList.append(row);
    return;
  }
  for (const team of state.teams) {
    const row = document.createElement("tr");

    const nameCell = document.createElement("td");
    const nameWrap = document.createElement("span");
    nameWrap.className = "team-table-name";
    const mark = document.createElement("span");
    mark.className = "team-mark";
    mark.textContent = memberInitials(team.name);
    const open = document.createElement("button");
    open.type = "button";
    open.className = "workspace-name-button";
    open.textContent = team.name;
    open.addEventListener("click", () => editTeam(team));
    nameWrap.append(mark, open);
    nameCell.append(nameWrap);

    const membersCell = document.createElement("td");
    const members = Number(team.memberCount) || 0;
    membersCell.textContent = `${members} member${members === 1 ? "" : "s"}`;

    const groupsCell = document.createElement("td");
    const groups = Number(team.groupCount) || 0;
    groupsCell.textContent = groups ? `${groups} group${groups === 1 ? "" : "s"}` : "—";

    const quotaCell = document.createElement("td");
    quotaCell.append(quotaChips(team.quota, "No limit"));

    const actionsCell = document.createElement("td");
    actionsCell.className = "workspace-table-actions";
    const actions = document.createElement("div");
    actions.className = "workspace-actions team-actions";
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
    actionsCell.append(actions);

    row.append(nameCell, membersCell, groupsCell, quotaCell, actionsCell);
    els.teamsList.append(row);
  }
}
async function openTeams() {
  try {
    state.teamQuery = "";
    els.teamsSearch.value = "";
    state.teamPage = 1;
    await Promise.all([refreshTeams({ paginated: true, page: 1, summary: true }), loadDirectory()]);
    renderTeamsPagination();
    showView("teams");
  } catch (err) {
    toast(err.message || "Failed to load teams.");
  }
}
async function editTeam(team) {
  if (!state.me.isAdmin) return;
  try {
    const detail = await api(`/api/teams/${encodeURIComponent(team.name)}`);
    state.editingTeam = detail.name;
    els.teamFormTitle.textContent = `Edit ${detail.name}`;
    els.teamName.value = detail.name;
    els.teamName.readOnly = true;
    const { users, groups } = splitTeamMembers(detail.members || []);
    entryLists.teamMembers.setValues(users);
    entryLists.teamGroups.setValues(groups);
    setQuotaInputs("team", detail.quota || null);
    setTeamQuotaEnabled(true);
    els.teamSave.textContent = "Save team";
    els.teamDialog.showModal();
  } catch (err) {
    toast(err.message || "Failed to load team.");
  }
}
function newTeam() {
  if (!state.me.isAdmin) return;
  state.editingTeam = null;
  els.teamForm.reset();
  els.teamFormTitle.textContent = "New team";
  entryLists.teamMembers.setValues([]);
  entryLists.teamGroups.setValues([]);
  setQuotaInputs("team", null);
  setTeamQuotaEnabled(true);
  els.teamName.readOnly = false;
  els.teamSave.textContent = "Create team";
  els.teamDialog.showModal();
  requestAnimationFrame(() => els.teamName.focus());
}
async function saveTeam(event) {
  event.preventDefault();
  const name = els.teamName.value.trim();
  if (!name) return;
  const members = combineTeamMembers(entryLists.teamMembers.getValues(), entryLists.teamGroups.getValues());
  let quota;
  try {
    quota = readQuota("team");
  } catch (err) {
    toast(err.message || "Invalid quota.");
    return;
  }

  const wasEditing = Boolean(state.editingTeam);
  const original = els.teamSave.innerHTML;
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
    els.teamDialog.close();
    const page = wasEditing ? state.teamPage : 1;
    await refreshTeams({ paginated: true, page, summary: true });
    renderTeamsPagination();
  } catch (err) {
    toast(err.message || "Failed to save team.");
  } finally {
    els.teamSave.disabled = false;
    els.teamSave.innerHTML = original;
  }
}
async function deleteTeam(team) {
  if (!window.confirm(`Delete team "${team.name}"? It will be removed from every template.`)) return;
  try {
    await api(`/api/teams/${encodeURIComponent(team.name)}`, { method: "DELETE" });
    toast(`Team ${team.name} deleted.`, "success");
    if (state.editingTeam === team.name) {
      state.editingTeam = null;
      els.teamDialog.close();
    }
    await refreshTeams({ paginated: true, page: state.teamPage, summary: true });
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
