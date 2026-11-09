"use strict";

function directoryURL(page, groupPage, query = "") {
  const params = new URLSearchParams({
    page: String(page),
    per_page: String(state.userPerPage),
    group_page: String(groupPage),
    group_per_page: String(state.groupPerPage),
  });
  if (query) params.set("q", query);
  return `/api/users?${params.toString()}`;
}
async function loadDirectory({ paginated = false, query = "", page = 1, groupPage = 1 } = {}) {
  try {
    const data = paginated
      ? await api(directoryURL(page, groupPage, query.trim()))
      : await api("/api/users");
    state.directory = {
      users: Array.isArray(data.users) ? data.users : [],
      groups: Array.isArray(data.groups) ? data.groups : [],
    };
    if (paginated) {
      state.userPage = Number(data.page) || 1;
      state.userPages = Number(data.pages) || 0;
      state.userTotal = Number(data.totalUsers) || state.directory.users.length;
      state.groupPage = Number(data.groupPage) || 1;
      state.groupPages = Number(data.groupPages) || 0;
      state.groupTotal = Number(data.totalGroups) || state.directory.groups.length;
      state.userPerPage = Number(data.perPage) || state.userPerPage;
      state.groupPerPage = Number(data.groupPerPage) || state.groupPerPage;
    } else {
      state.userPage = 1;
      state.userPages = 0;
      state.userTotal = state.directory.users.length;
      state.groupPage = 1;
      state.groupPages = 0;
      state.groupTotal = state.directory.groups.length;
    }
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

  const totalUsers = state.userPages ? state.userTotal : users.length;
  const totalGroups = state.groupPages ? state.groupTotal : groups.length;
  els.usersCount.textContent = `${totalUsers} user${totalUsers === 1 ? "" : "s"} · ${totalGroups} group${totalGroups === 1 ? "" : "s"}`;
  renderDirectoryList(els.usersList, users, memberships.users, "user", query);
  renderDirectoryList(els.groupsList, groups, memberships.groups, "group", query);
}
function renderUsersPagination() {
  renderPagination(els.usersPagination, {
    page: state.userPage,
    pages: state.userPages,
    total: state.userTotal,
    summary: `${state.userTotal} user${state.userTotal === 1 ? "" : "s"}`,
  }, (page) => {
    state.userPage = page;
    loadDirectory({ paginated: true, query: state.userQuery, page, groupPage: state.groupPage }).then(() => {
      renderUsers();
      renderUsersPagination();
      renderGroupsPagination();
    });
  });
}
function renderGroupsPagination() {
  renderPagination(els.groupsPagination, {
    page: state.groupPage,
    pages: state.groupPages,
    total: state.groupTotal,
    summary: `${state.groupTotal} group${state.groupTotal === 1 ? "" : "s"}`,
  }, (page) => {
    state.groupPage = page;
    loadDirectory({ paginated: true, query: state.userQuery, page: state.userPage, groupPage: page }).then(() => {
      renderUsers();
      renderUsersPagination();
      renderGroupsPagination();
    });
  });
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
    const quotaEntry = kind === "user" && state.me.isAdmin ? userQuotaEntry(value) : null;
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
    if (quotaEntry) {
      const summary = quotaSummary(quotaEntry.quota);
      if (summary) {
        const quota = document.createElement("span");
        quota.className = "directory-quota";
        quota.textContent = `Quota: ${summary}`;
        meta.append(quota);
      }
    }
    main.append(name, meta);
    item.append(avatar, main);
    if (kind === "user" && state.me.isAdmin) {
      const quota = document.createElement("button");
      quota.type = "button";
      quota.className = "button ghost small quota-button";
      quota.textContent = quotaEntry ? "Edit quota" : "Set quota";
      quota.addEventListener("click", () => openUserQuota(value, quotaEntry));
      item.append(quota);
    }
    container.append(item);
  }
}
async function openUsers() {
  try {
    state.userPage = 1;
    state.groupPage = 1;
    await Promise.all([loadDirectory({ paginated: true, page: 1, groupPage: 1 }), refreshTeams(), loadUserQuotas()]);
    state.userQuery = "";
    els.usersSearch.value = "";
    renderUsers();
    renderUsersPagination();
    renderGroupsPagination();
    showView("users");
  } catch (err) {
    toast(err.message || "Failed to load users.");
  }
}
