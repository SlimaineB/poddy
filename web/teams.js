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

function teamMemberSummary(team) {
  const members = Array.isArray(team.members) ? team.members : [];
  if (!members.length) {
    const count = Number(team.memberCount) || 0;
    return count ? `${count} member${count === 1 ? "" : "s"}` : "No members";
  }
  const { users, groups } = splitTeamMembers(members);
  const parts = [];
  if (users.length) parts.push(`${users.length} user${users.length === 1 ? "" : "s"}`);
  if (groups.length) parts.push(`${groups.length} OIDC group${groups.length === 1 ? "" : "s"}`);
  return parts.length ? parts.join(" · ") : "No members";
}

function renderTeamMembers(team) {
  const wrap = document.createElement("div");
  wrap.className = "team-card-members";
  const members = Array.isArray(team.members) ? team.members : [];
  if (!members.length) {
    const count = Number(team.memberCount) || 0;
    const empty = document.createElement("span");
    empty.className = "team-member-empty";
    empty.textContent = count
      ? `${count} member${count === 1 ? "" : "s"} · names visible to admins`
      : "No users or OIDC groups yet.";
    wrap.append(empty);
    return wrap;
  }

  const { users, groups } = splitTeamMembers(members);
  const ordered = [
    ...users.map((value) => ({ value, group: false })),
    ...groups.map((value) => ({ value: `group:${value}`, group: true })),
  ];

  const stack = document.createElement("span");
  stack.className = "team-avatar-stack";
  for (const entry of ordered.slice(0, 4)) {
    const avatar = document.createElement("span");
    avatar.className = "team-avatar";
    if (entry.group) avatar.classList.add("group");
    avatar.textContent = memberInitials(entry.value);
    avatar.title = entry.group ? `OIDC group: ${directoryGroup(entry.value)}` : entry.value;
    stack.append(avatar);
  }
  if (ordered.length > 4) {
    const more = document.createElement("span");
    more.className = "team-avatar more";
    more.textContent = `+${ordered.length - 4}`;
    stack.append(more);
  }

  const labels = document.createElement("span");
  labels.className = "team-member-names";
  const parts = [];
  if (users.length) {
    parts.push(users.slice(0, 3).join(", ") + (users.length > 3 ? ` +${users.length - 3}` : ""));
  }
  if (groups.length) {
    const names = groups.slice(0, 2).map((group) => `OIDC: ${group}`).join(", ");
    parts.push(names + (groups.length > 2 ? ` +${groups.length - 2}` : ""));
  }
  labels.textContent = parts.join(" · ");
  wrap.append(stack, labels);
  return wrap;
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
    item.className = "team-item team-card";

    const head = document.createElement("div");
    head.className = "team-card-head";
    const mark = document.createElement("span");
    mark.className = "team-mark";
    mark.textContent = memberInitials(team.name);

    const info = document.createElement("div");
    info.className = "team-card-info";
    const titleRow = document.createElement("div");
    titleRow.className = "team-card-title";
    const name = document.createElement("h3");
    name.textContent = team.name;
    const count = document.createElement("span");
    count.className = "badge neutral";
    count.textContent = teamMemberSummary(team);
    titleRow.append(name, count);
    info.append(titleRow, renderTeamMembers(team));

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
    head.append(mark, info, actions);

    const quota = document.createElement("div");
    quota.className = "team-card-quota";
    const label = document.createElement("span");
    label.className = "team-card-quota-label";
    label.textContent = "Default quota";
    quota.append(label, quotaChips(team.quota, "No limit"));

    item.append(head, quota);
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
  const { users, groups } = splitTeamMembers(team.members || []);
  entryLists.teamMembers.setValues(users);
  entryLists.teamGroups.setValues(groups);
  setQuotaInputs("team", team.quota || null);
  setTeamQuotaEnabled(state.me.isAdmin);
  els.teamSave.textContent = "Save team";
  els.teamForm.scrollIntoView({ behavior: "smooth", block: "start" });
}
function newTeam() {
  state.editingTeam = null;
  els.teamForm.reset();
  els.teamFormTitle.textContent = "New team";
  entryLists.teamMembers.setValues([]);
  entryLists.teamGroups.setValues([]);
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
  const members = combineTeamMembers(entryLists.teamMembers.getValues(), entryLists.teamGroups.getValues());
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
    const savedMembers = splitTeamMembers(current?.members || members);
    entryLists.teamMembers.setValues(savedMembers.users);
    entryLists.teamGroups.setValues(savedMembers.groups);
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
