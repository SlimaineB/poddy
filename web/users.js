"use strict";

async function loadDirectory() {
  try {
    const data = await api("/api/users");
    state.directory = {
      users: Array.isArray(data.users) ? data.users : [],
      groups: Array.isArray(data.groups) ? data.groups : [],
    };
  } catch {
    state.directory = { users: [], groups: [] };
  }
}
function directoryUser(entry) {
  return String(entry || "").replace(/^(user|email):/i, "").trim();
}
function directoryGroup(entry) {
  return String(entry || "").replace(/^group:/i, "").trim();
}
function renderUsers() {
  const query = state.userQuery.trim().toLowerCase();
  const memberships = teamMemberships();
  const users = state.directory.users.filter((user) => !query || user.toLowerCase().includes(query));
  const groups = state.directory.groups.filter((group) => !query || group.toLowerCase().includes(query));

  els.usersCount.textContent = `${users.length} user${users.length === 1 ? "" : "s"} · ${groups.length} group${groups.length === 1 ? "" : "s"}`;
  renderDirectoryList(els.usersList, users, memberships.users, "user", query);
  renderDirectoryList(els.groupsList, groups, memberships.groups, "group", query);
}
function renderDirectoryList(container, values, memberships, kind, query) {
  container.replaceChildren();
  if (!values.length) {
    container.append(emptyInline(query ? `No ${kind}s match your search.` : `No ${kind}s yet.`));
    return;
  }
  for (const value of values) {
    const item = document.createElement("div");
    item.className = "user-item";
    const avatar = document.createElement("span");
    avatar.className = "directory-avatar";
    avatar.textContent = kind === "group" ? "#" : value.slice(0, 1).toUpperCase();
    const main = document.createElement("span");
    main.className = "directory-main";
    const name = document.createElement("span");
    name.className = "directory-name";
    name.textContent = value;
    const meta = document.createElement("span");
    meta.className = "directory-meta";
    const teams = memberships.get(value.toLowerCase()) || [];
    if (kind === "group") {
      const badge = document.createElement("span");
      badge.className = "badge neutral";
      badge.textContent = "group";
      meta.append(badge);
    }
    if (teams.length) {
      for (const team of teams) {
        const badge = document.createElement("span");
        badge.className = "badge owner";
        badge.textContent = team;
        meta.append(badge);
      }
    } else {
      const none = document.createElement("span");
      none.textContent = state.me.isAdmin
        ? (kind === "group" ? "No team uses this group." : "Not in a team.")
        : "Team memberships are visible to admins.";
      meta.append(none);
    }
    main.append(name, meta);
    item.append(avatar, main);
    container.append(item);
  }
}
async function openUsers() {
  try {
    await Promise.all([loadDirectory(), refreshTeams()]);
    state.userQuery = "";
    els.usersSearch.value = "";
    renderUsers();
    showView("users");
  } catch (err) {
    toast(err.message || "Failed to load users.");
  }
}
