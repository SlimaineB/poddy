"use strict";

const token = new URLSearchParams(location.search).get("token") || "";

const state = {
  config: {},
  me: {},
  templates: [],
  teams: [],
  workspaces: [],
  directory: { users: [], groups: [] },
  workspaceQuery: "",
  workspaceView: (() => {
    try {
      return localStorage.getItem("poddy.workspaceView") === "list" ? "list" : "cards";
    } catch {
      return "cards";
    }
  })(),
  templateQuery: "",
  userQuery: "",
  loaded: false,
  pollError: false,
  sshWorkspace: null,
  pendingDelete: null,
  accessTemplate: null,
  accessCanEditOwners: false,
  editingTeam: null,
  externalAuth: [],
  externalAuthEnabled: false,
};

const els = {
  grid: document.getElementById("grid"),
  workspaceListWrap: document.getElementById("workspace-list-wrap"),
  workspaceList: document.getElementById("workspace-list"),
  workspaceSearch: document.getElementById("workspace-search"),
  workspaceViewCards: document.getElementById("workspace-view-cards"),
  workspaceViewList: document.getElementById("workspace-view-list"),
  empty: document.getElementById("empty"),
  emptyTitle: document.getElementById("empty-title"),
  emptyText: document.getElementById("empty-text"),
  loading: document.getElementById("loading"),
  count: document.getElementById("count"),
  toasts: document.getElementById("toasts"),
  sshEndpoint: document.getElementById("ssh-endpoint"),
  userChip: document.getElementById("user-chip"),
  userName: document.getElementById("user-name"),
  userAvatar: document.getElementById("user-avatar"),
  logoutForm: document.getElementById("logout-form"),
  refresh: document.getElementById("refresh"),
  newWorkspace: document.getElementById("new-workspace"),
  createDialog: document.getElementById("create-dialog"),
  createForm: document.getElementById("create-form"),
  createSubmit: document.getElementById("create-submit"),
  nameInput: document.getElementById("workspace-name"),
  templateSelect: document.getElementById("template-select"),
  templateDescription: document.getElementById("template-description"),
  params: document.getElementById("params"),
  sshDialog: document.getElementById("ssh-dialog"),
  sshConfig: document.getElementById("ssh-config"),
  sshNote: document.getElementById("ssh-note"),
  openVSCode: document.getElementById("open-vscode"),
  copySSH: document.getElementById("copy-ssh"),
  detailDialog: document.getElementById("detail-dialog"),
  detailTitle: document.getElementById("detail-title"),
  detailList: document.getElementById("detail-list"),
  confirmDialog: document.getElementById("confirm-dialog"),
  confirmText: document.getElementById("confirm-text"),
  confirmDelete: document.getElementById("confirm-delete"),
  manageWorkspaces: document.getElementById("manage-workspaces"),
  manageConnections: document.getElementById("manage-connections"),
  manageUsers: document.getElementById("manage-users"),
  manageTeams: document.getElementById("manage-teams"),
  manageTemplates: document.getElementById("manage-templates"),
  userRole: document.getElementById("user-role"),
  workspacesView: document.getElementById("workspaces-view"),
  usersView: document.getElementById("users-view"),
  usersBack: document.getElementById("users-back"),
  usersSearch: document.getElementById("users-search"),
  usersCount: document.getElementById("users-count"),
  usersList: document.getElementById("users-list"),
  groupsList: document.getElementById("groups-list"),
  connectionsView: document.getElementById("connections-view"),
  connectionsBack: document.getElementById("connections-back"),
  connectionsCount: document.getElementById("connections-count"),
  connectionsList: document.getElementById("connections-list"),
  externalAuthNotice: document.getElementById("external-auth-notice"),
  teamsView: document.getElementById("teams-view"),
  teamsBack: document.getElementById("teams-back"),
  teamsCount: document.getElementById("teams-count"),
  templatesView: document.getElementById("templates-view"),
  templateDetailView: document.getElementById("template-detail-view"),
  templatesBack: document.getElementById("templates-back"),
  templateDetailBack: document.getElementById("template-detail-back"),
  templatesSearch: document.getElementById("templates-search"),
  templatesPageList: document.getElementById("templates-page-list"),
  templatesCount: document.getElementById("templates-count"),
  templateDetailTitle: document.getElementById("template-detail-title"),
  templateDetailDescription: document.getElementById("template-detail-description"),
  templateAccessForm: document.getElementById("template-access-form"),
  accessOwners: document.getElementById("access-owners"),
  accessUsers: document.getElementById("access-users"),
  accessTeams: document.getElementById("access-teams"),
  accessSave: document.getElementById("access-save"),
  teamsList: document.getElementById("teams-list"),
  teamForm: document.getElementById("team-form"),
  teamFormTitle: document.getElementById("team-form-title"),
  teamName: document.getElementById("team-name"),
  teamMembers: document.getElementById("team-members"),
  teamNew: document.getElementById("team-new"),
  teamSave: document.getElementById("team-save"),
};

function withToken(url) {
  if (!token) return url;
  return `${url}${url.includes("?") ? "&" : "?"}token=${encodeURIComponent(token)}`;
}
function icon(name, className = "icon") {
  return `<svg class="${className}" aria-hidden="true"><use href="#icon-${name}"></use></svg>`;
}
async function api(path, options = {}) {
  const headers = { ...(options.headers || {}) };
  if (token) headers.Authorization = `Bearer ${token}`;
  if (options.body) headers["Content-Type"] = "application/json";

  const res = await fetch(path, { ...options, headers });
  if (res.status === 401) {
    location.href = `/login?next=${encodeURIComponent(location.pathname + location.search)}`;
    throw new Error("Your session has expired.");
  }
  if (!res.ok) {
    const text = (await res.text()).trim();
    let message = text;
    try {
      const data = JSON.parse(text);
      message = data.error || message;
    } catch {
      // keep plain text
    }
    throw new Error(message || res.statusText);
  }
  if (res.status === 204) return null;
  return res.json();
}
function toast(message, type = "error") {
  const node = document.createElement("div");
  node.className = `toast${type === "success" ? " success" : ""}`;
  node.innerHTML = `${icon(type === "success" ? "check" : "alert")}<div class="toast-message"></div><button class="toast-close" type="button" aria-label="Dismiss">${icon("x")}</button>`;
  node.querySelector(".toast-message").textContent = message;

  const close = () => node.remove();
  node.querySelector(".toast-close").addEventListener("click", close);
  while (els.toasts.children.length >= 3) els.toasts.firstElementChild.remove();
  els.toasts.append(node);
  setTimeout(close, 6500);
}
function humanize(name) {
  const known = {
    cpu: "CPU",
    memory: "Memory",
    storage: "Storage",
    storageClass: "Storage class",
    image: "Image",
  };
  if (known[name]) return known[name];
  return name
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/^./, (c) => c.toUpperCase());
}
function templateByName(name) {
  return state.templates.find((t) => t.name === name);
}
function statusClass(phase) {
  const value = String(phase || "").toLowerCase();
  if (value === "running") return "running";
  if (value === "pending" || value === "containercreating" || value === "podinitializing") return "pending";
  if (value === "failed" || value === "error" || value === "crashloopbackoff" || value === "imagepullbackoff") return "failed";
  if (value === "terminating") return "terminating";
  if (value === "stopped") return "stopped";
  return "";
}
function formatAge(iso) {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime()) || date.getFullYear() < 2000) return "";
  const seconds = Math.max(0, (Date.now() - date.getTime()) / 1000);
  for (const [suffix, size] of [["d", 86400], ["h", 3600], ["m", 60]]) {
    if (seconds >= size) return `${Math.floor(seconds / size)}${suffix} ago`;
  }
  return "just now";
}
function vscodeURL(name) {
  return `vscode://vscode-remote/ssh-remote+poddy-ssh-${encodeURIComponent(name)}/home/dev`;
}
function sshConfigFor() {
  const keyPath = state.config.windowsSSHKeyPath || state.config.sshKeyPath || "~/.ssh/id_ed25519";
  const nullFile = /Windows/i.test(navigator.userAgent) ? "NUL" : "/dev/null";
  return `# Poddy: works for all workspaces
Host poddy-ssh-*
    User dev
    ProxyJump poddy-portal
    IdentityFile ${keyPath}
    IdentitiesOnly yes
    StrictHostKeyChecking no
    UserKnownHostsFile ${nullFile}
    LogLevel ERROR

Host poddy-portal
    HostName ${state.config.sshHost || "127.0.0.1"}
    Port ${state.config.sshPort || "2222"}
    User poddy
    IdentityFile ${keyPath}
    IdentitiesOnly yes
    StrictHostKeyChecking accept-new
    LogLevel ERROR
`;
}
function renderConfig() {
  if (!state.config.ssh) return;
  els.sshEndpoint.hidden = false;
  els.sshEndpoint.textContent = `SSH ${state.config.sshHost || "127.0.0.1"}:${state.config.sshPort || "2222"}`;
}
function renderTemplateOptions() {
  els.templateSelect.replaceChildren();
  for (const template of state.templates) {
    const option = document.createElement("option");
    option.value = template.name;
    option.textContent = template.displayName || template.name;
    els.templateSelect.append(option);
  }
  if (state.templates.length) {
    els.templateSelect.value = state.templates[0].name;
  }
  renderParams();
}
function renderParams() {
  const template = templateByName(els.templateSelect.value);
  els.templateDescription.textContent = template ? template.description || "" : "";
  els.params.replaceChildren();
  if (!template) return;

  renderExternalAuthNotice();

  for (const parameter of template.parameters || []) {
    const field = document.createElement("div");
    field.className = "field";

    const label = document.createElement("label");
    label.htmlFor = `param-${parameter.name}`;
    label.textContent = humanize(parameter.name);

    const input = document.createElement("input");
    input.id = `param-${parameter.name}`;
    input.name = parameter.name;
    input.value = parameter.default || "";
    input.placeholder = parameter.required ? "Required" : "Optional";
    input.autocomplete = "off";
    input.spellcheck = false;
    if (parameter.required) input.required = true;

    field.append(label, input);
    if (parameter.description) {
      const hint = document.createElement("p");
      hint.className = "hint";
      hint.textContent = parameter.description;
      field.append(hint);
    }
    els.params.append(field);
  }
}
function closeDialog(button) {
  button.closest("dialog")?.close();
}
function emptyInline(message) {
  const node = document.createElement("div");
  node.className = "empty-inline";
  node.textContent = message;
  return node;
}
function createEntryList(container, { placeholder = "", addLabel = "Add" } = {}) {
  container.classList.add("entry-list");
  container.replaceChildren();

  const table = document.createElement("div");
  table.className = "entry-table";
  const empty = document.createElement("div");
  empty.className = "entry-empty";
  empty.textContent = "No entries yet.";

  const addRow = document.createElement("div");
  addRow.className = "entry-add";
  const input = document.createElement("input");
  input.type = "text";
  input.className = "entry-input";
  input.placeholder = placeholder;
  input.autocomplete = "off";
  input.spellcheck = false;
  const addButton = document.createElement("button");
  addButton.type = "button";
  addButton.className = "button small";
  addButton.textContent = addLabel;
  const menu = document.createElement("div");
  menu.className = "entry-suggestions";
  menu.hidden = true;
  addRow.append(input, addButton, menu);
  container.append(table, addRow);

  let values = [];
  let readOnly = false;
  let active = -1;
  let suggestions = [];

  const clean = (value) => String(value || "").trim();
  const same = (a, b) => a.toLowerCase() === b.toLowerCase();

  function render() {
    table.replaceChildren();
    if (!values.length) {
      table.append(empty);
    } else {
      values.forEach((value, index) => {
        const row = document.createElement("div");
        row.className = "entry-row";
        const text = document.createElement("span");
        text.className = "entry-value";
        text.textContent = value;
        row.append(text);
        if (!readOnly) {
          const remove = document.createElement("button");
          remove.type = "button";
          remove.className = "button danger small";
          remove.textContent = "Remove";
          remove.addEventListener("click", () => removeValue(index));
          row.append(remove);
        }
        table.append(row);
      });
    }
    input.disabled = readOnly;
    addButton.disabled = readOnly;
    addRow.hidden = readOnly;
    if (readOnly) menu.hidden = true;
  }

  function allSuggestions() {
    const users = Array.isArray(state.directory.users) ? state.directory.users : [];
    const groups = Array.isArray(state.directory.groups) ? state.directory.groups.map((group) => `group:${group}`) : [];
    return [...users, ...groups];
  }

  function updateSuggestions() {
    if (readOnly) {
      menu.hidden = true;
      return;
    }
    const query = input.value.trim().toLowerCase();
    suggestions = allSuggestions()
      .filter((value) => !values.some((selected) => same(selected, value)))
      .filter((value) => !query || value.toLowerCase().includes(query))
      .slice(0, 50);
    active = -1;
    menu.replaceChildren();
    if (!suggestions.length) {
      menu.hidden = true;
      return;
    }
    for (const value of suggestions) {
      const option = document.createElement("button");
      option.type = "button";
      option.className = "entry-suggestion";
      option.textContent = value;
      option.addEventListener("mousedown", (event) => {
        event.preventDefault();
        addValue(value);
      });
      menu.append(option);
    }
    menu.hidden = false;
  }

  function highlight(index) {
    const items = [...menu.querySelectorAll(".entry-suggestion")];
    items.forEach((item, itemIndex) => item.classList.toggle("active", itemIndex === index));
    items[index]?.scrollIntoView({ block: "nearest" });
  }

  function addValue(value) {
    value = clean(value);
    if (!value || readOnly) return;
    if (!values.some((selected) => same(selected, value))) values.push(value);
    input.value = "";
    render();
    updateSuggestions();
    input.focus();
  }

  function removeValue(index) {
    if (readOnly) return;
    values.splice(index, 1);
    render();
    updateSuggestions();
    input.focus();
  }

  input.addEventListener("input", updateSuggestions);
  input.addEventListener("focus", updateSuggestions);
  input.addEventListener("blur", () => setTimeout(() => { menu.hidden = true; }, 120));
  input.addEventListener("keydown", (event) => {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      if (!menu.hidden) {
        active = Math.min(active + 1, suggestions.length - 1);
        highlight(active);
      }
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      if (!menu.hidden) {
        active = Math.max(active - 1, 0);
        highlight(active);
      }
    } else if (event.key === "Enter") {
      event.preventDefault();
      if (active >= 0 && suggestions[active]) addValue(suggestions[active]);
      else if (input.value.trim()) addValue(input.value);
    } else if (event.key === "Escape") {
      menu.hidden = true;
    }
  });
  addButton.addEventListener("click", () => addValue(input.value));

  function setValues(next) {
    values = [];
    for (const value of next || []) {
      const cleaned = clean(value);
      if (cleaned && !values.some((selected) => same(selected, cleaned))) values.push(cleaned);
    }
    render();
  }

  function setReadOnly(value) {
    readOnly = Boolean(value);
    render();
  }

  render();
  return {
    setValues,
    getValues: () => [...values],
    setReadOnly,
    focus: () => input.focus(),
  };
}

const entryLists = {
  owners: createEntryList(els.accessOwners, { placeholder: "owner@example.com", addLabel: "Add owner" }),
  users: createEntryList(els.accessUsers, { placeholder: "alice@example.com", addLabel: "Add user" }),
  teamMembers: createEntryList(els.teamMembers, { placeholder: "alice@example.com or group:backend", addLabel: "Add member" }),
};
async function loadMe() {
  try {
    const res = await fetch("/api/me", {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    });
    if (!res.ok) return;
    const me = await res.json();
    if (!me || !me.username) return;
    state.me = me;
    els.userChip.hidden = false;
    els.logoutForm.hidden = false;
    els.userName.textContent = me.username;
    els.userAvatar.textContent = me.username.slice(0, 1).toUpperCase();
    if (me.email) els.userChip.title = me.email;

    const owned = Array.isArray(me.ownedTemplates) ? me.ownedTemplates : [];
    els.userRole.hidden = true;
    els.userRole.classList.remove("owner");
    els.manageUsers.hidden = true;
    els.manageTeams.hidden = true;
    els.manageTemplates.hidden = true;
    if (me.isAdmin) {
      els.userRole.hidden = false;
      els.userRole.textContent = "admin";
      els.manageUsers.hidden = false;
      els.manageTeams.hidden = false;
      els.manageTemplates.hidden = false;
    } else if (owned.length) {
      els.userRole.hidden = false;
      els.userRole.textContent = "owner";
      els.userRole.classList.add("owner");
      els.manageUsers.hidden = false;
      els.manageTemplates.hidden = false;
    }
  } catch {
    // The server redirects unauthenticated page loads to the login page.
  }
}
function showView(name) {
  els.workspacesView.hidden = name !== "workspaces";
  els.connectionsView.hidden = name !== "connections";
  els.usersView.hidden = name !== "users";
  els.teamsView.hidden = name !== "teams";
  els.templatesView.hidden = name !== "templates";
  els.templateDetailView.hidden = name !== "template";
  const active = name === "workspaces" ? els.manageWorkspaces
    : name === "connections" ? els.manageConnections
    : name === "users" ? els.manageUsers
    : name === "teams" ? els.manageTeams
    : (name === "templates" || name === "template") ? els.manageTemplates
    : null;
  for (const button of [els.manageWorkspaces, els.manageConnections, els.manageUsers, els.manageTeams, els.manageTemplates]) {
    if (!button) continue;
    button.classList.toggle("active", button === active);
    if (button === active) button.setAttribute("aria-current", "page");
    else button.removeAttribute("aria-current");
  }
  window.scrollTo({ top: 0 });
}
