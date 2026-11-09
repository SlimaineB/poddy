"use strict";

const token = new URLSearchParams(location.search).get("token") || "";

const state = {
  config: {},
  me: {},
  templates: [],
  teams: [],
  workspaces: [],
  topology: { totals: {}, users: [] },
  directory: { users: [], groups: [] },
  workspaceQuery: "",
  workspacePage: 1,
  workspacePerPage: 12,
  workspaceTotal: 0,
  workspacePages: 0,
  topologyPage: 1,
  topologyPerPage: 10,
  topologyQuery: "",
  userPage: 1,
  userPerPage: 20,
  userTotal: 0,
  userPages: 0,
  groupPage: 1,
  groupPerPage: 20,
  groupTotal: 0,
  groupPages: 0,
  teamPage: 1,
  teamPerPage: 20,
  teamTotal: 0,
  teamPages: 0,
  templatePage: 1,
  templatePerPage: 20,
  templateTotal: 0,
  templatePages: 0,
  templatePageItems: [],
  templatePageLoaded: false,
  topologyActive: false,
  mapActive: false,
  mapSelected: "",
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
  detailWorkspace: null,
  detailRequest: 0,
  manifests: null,
  manifestTab: "pod",
  detailPortsRequest: 0,
  portsWorkspace: null,
  portsRequest: 0,
  accessTemplate: null,
  accessCanEditOwners: false,
  editingTeam: null,
  userQuotas: [],
  quotaUser: "",
  quotaIdentity: "",
  externalAuth: [],
  externalAuthEnabled: false,
  sshKeys: [],
  regionFilter: new URLSearchParams(location.search).get("region") || "",
  regionSummaries: [],
  regionSummaryRequest: 0,
  volumes: [],
  volumeSummary: {},
  volumeQuery: "",
  volumeFilter: "all",
  volumeBackupEnabled: false,
  volumeBackupVolume: null,
  volumeBackupRequest: 0,
  volumeBackups: [],
  volumeRestoreTargets: [],
  volumeRestoreKey: "",
  volumeRestoreName: "",
  volumeRestoreClean: false,
  pendingVolumeDelete: null,
  auditItems: [],
  auditPage: 1,
  auditPerPage: 50,
  auditTotal: 0,
  auditPages: 0,
  auditQuery: "",
  auditOutcome: "",
  auditAction: "",
  auditSince: "",
  auditUntil: "",
};

const els = {
  grid: document.getElementById("grid"),
  workspaceListWrap: document.getElementById("workspace-list-wrap"),
  workspaceList: document.getElementById("workspace-list"),
  workspaceSearch: document.getElementById("workspace-search"),
  empty: document.getElementById("empty"),
  emptyTitle: document.getElementById("empty-title"),
  emptyText: document.getElementById("empty-text"),
  loading: document.getElementById("loading"),
  count: document.getElementById("count"),
  workspacesPagination: document.getElementById("workspaces-pagination"),
  topologyPagination: document.getElementById("topology-pagination"),
  topologySearch: document.getElementById("topology-search"),
  usersPagination: document.getElementById("users-pagination"),
  groupsPagination: document.getElementById("groups-pagination"),
  teamsPagination: document.getElementById("teams-pagination"),
  templatesPagination: document.getElementById("templates-pagination"),
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
  nameHint: document.getElementById("workspace-name-hint"),
  regionField: document.getElementById("region-field"),
  regionSelect: document.getElementById("region-select"),
  regionSwitcher: document.getElementById("region-switcher"),
  regionFilter: document.getElementById("region-filter"),
  regionInfo: document.getElementById("region-info"),
  regionsDialog: document.getElementById("regions-dialog"),
  regionsList: document.getElementById("regions-list"),
  regionsShowAll: document.getElementById("regions-show-all"),
  templateSelect: document.getElementById("template-select"),
  templateDescription: document.getElementById("template-description"),
  createError: document.getElementById("create-error"),
  params: document.getElementById("params"),
  sshDialog: document.getElementById("ssh-dialog"),
  sshConfig: document.getElementById("ssh-config"),
  sshNote: document.getElementById("ssh-note"),
  openVSCode: document.getElementById("open-vscode"),
  openVSCodeWeb: document.getElementById("open-vscode-web"),
  copySSH: document.getElementById("copy-ssh"),
  detailDialog: document.getElementById("detail-dialog"),
  detailTitle: document.getElementById("detail-title"),
  detailList: document.getElementById("detail-list"),
  detailLogsBody: document.getElementById("detail-logs-body"),
  detailLogsRefresh: document.getElementById("detail-logs-refresh"),
  detailManifests: document.getElementById("detail-manifests"),
  detailManifestsNote: document.getElementById("detail-manifests-note"),
  detailManifestsBody: document.getElementById("detail-manifests-body"),
  detailManifestsRefresh: document.getElementById("detail-manifests-refresh"),
  detailManifestsCopy: document.getElementById("detail-manifests-copy"),
  detailPortsBody: document.getElementById("detail-ports-body"),
  detailPortsRefresh: document.getElementById("detail-ports-refresh"),
  detailPortForm: document.getElementById("detail-port-form"),
  detailPortInput: document.getElementById("detail-port-input"),
  portsDialog: document.getElementById("ports-dialog"),
  portsTitle: document.getElementById("ports-title"),
  portsSubtitle: document.getElementById("ports-subtitle"),
  portsBody: document.getElementById("ports-body"),
  portsRefresh: document.getElementById("ports-refresh"),
  portsForm: document.getElementById("ports-form"),
  portsInput: document.getElementById("ports-input"),
  confirmDialog: document.getElementById("confirm-dialog"),
  confirmText: document.getElementById("confirm-text"),
  confirmDelete: document.getElementById("confirm-delete"),
  manageWorkspaces: document.getElementById("manage-workspaces"),
  manageVolumes: document.getElementById("manage-volumes"),
  manageConnections: document.getElementById("manage-connections"),
  manageUsers: document.getElementById("manage-users"),
  manageTeams: document.getElementById("manage-teams"),
  manageTemplates: document.getElementById("manage-templates"),
  manageAudit: document.getElementById("manage-audit"),
  userRole: document.getElementById("user-role"),
  workspacesView: document.getElementById("workspaces-view"),
  auditView: document.getElementById("audit-view"),
  auditBack: document.getElementById("audit-back"),
  auditRefresh: document.getElementById("audit-refresh"),
  auditSearch: document.getElementById("audit-search"),
  auditOutcome: document.getElementById("audit-outcome"),
  auditAction: document.getElementById("audit-action"),
  auditSince: document.getElementById("audit-since"),
  auditUntil: document.getElementById("audit-until"),
  auditApply: document.getElementById("audit-apply"),
  auditClear: document.getElementById("audit-clear"),
  auditCount: document.getElementById("audit-count"),
  auditLoading: document.getElementById("audit-loading"),
  auditEmpty: document.getElementById("audit-empty"),
  auditListWrap: document.getElementById("audit-list-wrap"),
  auditList: document.getElementById("audit-list"),
  auditPagination: document.getElementById("audit-pagination"),
  volumesView: document.getElementById("volumes-view"),
  volumesBack: document.getElementById("volumes-back"),
  volumesRefresh: document.getElementById("volumes-refresh"),
  volumesSearch: document.getElementById("volumes-search"),
  volumesSummary: document.getElementById("volumes-summary"),
  volumesCount: document.getElementById("volumes-count"),
  volumesLoading: document.getElementById("volumes-loading"),
  volumesEmpty: document.getElementById("volumes-empty"),
  volumesListWrap: document.getElementById("volumes-list-wrap"),
  volumesList: document.getElementById("volumes-list"),
  volumeBackupsDialog: document.getElementById("volume-backups-dialog"),
  volumeBackupsTitle: document.getElementById("volume-backups-title"),
  volumeBackupsSubtitle: document.getElementById("volume-backups-subtitle"),
  volumeBackupsList: document.getElementById("volume-backups-list"),
  volumeBackupsRefresh: document.getElementById("volume-backups-refresh"),
  volumeBackupCreate: document.getElementById("volume-backup-create"),
  volumeBackupStatus: document.getElementById("volume-backup-status"),
  restoreDialog: document.getElementById("restore-dialog"),
  restoreText: document.getElementById("restore-text"),
  restoreTarget: document.getElementById("restore-target"),
  restoreTargetHint: document.getElementById("restore-target-hint"),
  restoreClean: document.getElementById("restore-clean"),
  restoreConfirm: document.getElementById("restore-confirm"),
  restoreError: document.getElementById("restore-error"),
  volumeDeleteDialog: document.getElementById("volume-delete-dialog"),
  volumeDeleteText: document.getElementById("volume-delete-text"),
  volumeDeleteConfirm: document.getElementById("volume-delete-confirm"),
  topologyView: document.getElementById("topology-view"),
  topologyCount: document.getElementById("topology-count"),
  topologyLoading: document.getElementById("topology-loading"),
  topologyEmpty: document.getElementById("topology-empty"),
  topologyGraph: document.getElementById("topology-graph"),
  topologyHint: document.getElementById("topology-hint"),
  topologyFit: document.getElementById("topology-fit"),
  topologyRelayout: document.getElementById("topology-relayout"),
  topologyZoomIn: document.getElementById("topology-zoom-in"),
  topologyZoomOut: document.getElementById("topology-zoom-out"),
  mapView: document.getElementById("map-view"),
  mapCount: document.getElementById("map-count"),
  mapEmpty: document.getElementById("map-empty"),
  mapContainer: document.getElementById("map-container"),
  mapSvg: document.getElementById("world-map"),
  mapGrid: document.getElementById("map-grid"),
  mapLand: document.getElementById("map-land"),
  mapMarkers: document.getElementById("map-markers"),
  mapDetail: document.getElementById("map-detail"),
  mapRegionsList: document.getElementById("map-regions-list"),
  mapReset: document.getElementById("map-reset"),
  mapZoomIn: document.getElementById("map-zoom-in"),
  mapZoomOut: document.getElementById("map-zoom-out"),
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
  sshKeysView: document.getElementById("ssh-keys-view"),
  sshKeysBack: document.getElementById("ssh-keys-back"),
  sshKeysCount: document.getElementById("ssh-keys-count"),
  sshKeysList: document.getElementById("ssh-keys-list"),
  sshKeyForm: document.getElementById("ssh-key-form"),
  sshKeyInput: document.getElementById("ssh-key-input"),
  sshKeyFile: document.getElementById("ssh-key-file"),
  sshKeySave: document.getElementById("ssh-key-save"),
  sshKeyError: document.getElementById("ssh-key-error"),
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
  teamQuotaWorkspaces: document.getElementById("team-quota-workspaces"),
  teamQuotaCPU: document.getElementById("team-quota-cpu"),
  teamQuotaMemory: document.getElementById("team-quota-memory"),
  teamQuotaStorage: document.getElementById("team-quota-storage"),
  teamQuotaHint: document.getElementById("team-quota-hint"),
  quotaDialog: document.getElementById("user-quota-dialog"),
  quotaForm: document.getElementById("user-quota-form"),
  quotaIdentity: document.getElementById("user-quota-identity"),
  quotaWorkspaces: document.getElementById("user-quota-workspaces"),
  quotaCPU: document.getElementById("user-quota-cpu"),
  quotaMemory: document.getElementById("user-quota-memory"),
  quotaStorage: document.getElementById("user-quota-storage"),
  quotaSave: document.getElementById("user-quota-save"),
  quotaError: document.getElementById("user-quota-error"),
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
function pageURL(path, page, perPage, query = "") {
  const params = new URLSearchParams({ page: String(page), per_page: String(perPage) });
  if (query) params.set("q", query);
  if (state.regionFilter) params.set("region", state.regionFilter);
  return `${path}${path.includes("?") ? "&" : "?"}${params.toString()}`;
}
function renderPagination(container, meta, onPage) {
  if (!container) return;
  container.replaceChildren();
  const page = Number(meta?.page) || 1;
  const pages = Number(meta?.pages) || 0;
  if (pages <= 1) {
    container.hidden = true;
    return;
  }
  container.hidden = false;

  const info = document.createElement("span");
  info.className = "pagination-info";
  const total = Number(meta?.total) || 0;
  info.textContent = meta?.summary || `${total} item${total === 1 ? "" : "s"} · page ${page} of ${pages}`;

  const controls = document.createElement("span");
  controls.className = "pagination-controls";
  const previous = document.createElement("button");
  previous.type = "button";
  previous.className = "button ghost small";
  previous.textContent = "Previous";
  previous.disabled = page <= 1;
  previous.addEventListener("click", () => onPage(page - 1));
  const next = document.createElement("button");
  next.type = "button";
  next.className = "button ghost small";
  next.textContent = "Next";
  next.disabled = page >= pages;
  next.addEventListener("click", () => onPage(page + 1));
  controls.append(previous, next);
  container.append(info, controls);
}
function templateByName(name) {
  return state.templates.find((t) => t.name === name);
}
function statusClass(phase) {
  const value = String(phase || "").toLowerCase();
  if (value === "running") return "running";
  if (value === "pending" || value === "waiting" || value === "containercreating" || value === "podinitializing") return "pending";
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
function regionSlug(name) {
  return String(name || "").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "") || "region";
}
function sshAlias(name, region) {
  return region ? `poddy-ssh-${regionSlug(region)}-${name}` : `poddy-ssh-${name}`;
}
function vscodeURL(workspace) {
  const name = typeof workspace === "string" ? workspace : workspace.name;
  const region = typeof workspace === "string" ? "" : workspace.region;
  return `vscode://vscode-remote/ssh-remote+${sshAlias(name, region)}/home/dev?windowId=_blank`;
}
function browserVSCodeURL(name) {
  return `/api/workspaces/${encodeURIComponent(name)}/code/`;
}
function sshConfigFor() {
  const keyPath = state.config.windowsSSHKeyPath || state.config.sshKeyPath || "~/.ssh/id_ed25519";
  const nullFile = /Windows/i.test(navigator.userAgent) ? "NUL" : "/dev/null";
  const regions = Array.isArray(state.config.regions) && state.config.regions.length
    ? state.config.regions
    : [{ name: "", sshHost: state.config.sshHost, sshPort: state.config.sshPort }];
  let config = "# Poddy: works for all workspaces\n";
  for (const region of regions) {
    const slug = region.name ? `${regionSlug(region.name)}-` : "";
    const proxy = region.name ? `poddy-portal-${regionSlug(region.name)}` : "poddy-portal";
    config += `Host poddy-ssh-${slug}*\n`;
    config += "    User dev\n";
    config += `    ProxyJump ${proxy}\n`;
    config += `    IdentityFile ${keyPath}\n`;
    config += "    IdentitiesOnly yes\n    StrictHostKeyChecking no\n";
    config += `    UserKnownHostsFile ${nullFile}\n    LogLevel ERROR\n\n`;
  }
  for (const region of regions) {
    const slug = region.name ? `${regionSlug(region.name)}-` : "";
    const proxy = region.name ? `poddy-portal-${regionSlug(region.name)}` : "poddy-portal";
    config += `Host ${proxy}\n`;
    config += `    HostName ${region.sshHost || state.config.sshHost || "127.0.0.1"}\n`;
    config += `    Port ${region.sshPort || state.config.sshPort || "2222"}\n`;
    config += "    User poddy\n";
    config += `    IdentityFile ${keyPath}\n`;
    config += "    IdentitiesOnly yes\n    StrictHostKeyChecking accept-new\n    LogLevel ERROR\n\n";
    void slug;
  }
  return config;
}
function renderConfig() {
  renderRegionOptions();
  renderRegionFilter();
  if (!state.config.ssh) return;
  els.sshEndpoint.hidden = false;
  const regions = configuredRegions();
  els.sshEndpoint.textContent = regions.length > 1
    ? `SSH ${regions.length} regions`
    : `SSH ${state.config.sshHost || "127.0.0.1"}:${state.config.sshPort || "2222"}`;
}
function configuredRegions() {
  return Array.isArray(state.config.regions) ? state.config.regions : [];
}
function regionDisplayName(name) {
  const region = configuredRegions().find((item) => item.name === name);
  return region ? region.displayName || region.name : name;
}
function regionSummaryFor(name) {
  return (state.regionSummaries || []).find((item) => item.name === name) || null;
}
function syncRegionURL() {
  const url = new URL(location.href);
  if (state.regionFilter) url.searchParams.set("region", state.regionFilter);
  else url.searchParams.delete("region");
  history.replaceState({}, "", url);
}
function renderRegionFilter() {
  if (!els.regionSwitcher || !els.regionFilter) return;
  const regions = configuredRegions();
  if (!regions.some((region) => region.name === state.regionFilter)) {
    state.regionFilter = "";
  }
  els.regionSwitcher.hidden = regions.length === 0;
  if (!regions.length) {
    syncRegionURL();
    renderRegionSummary();
    return;
  }

  els.regionFilter.replaceChildren();
  const all = document.createElement("option");
  all.value = "";
  all.textContent = "All regions";
  els.regionFilter.append(all);
  for (const region of regions) {
    const option = document.createElement("option");
    option.value = region.name;
    option.textContent = region.displayName || region.name;
    els.regionFilter.append(option);
  }
  els.regionFilter.value = state.regionFilter;
  syncRegionURL();
  renderRegionSummary();
}
function setRegionFilter(name, { reload = true } = {}) {
  const regions = configuredRegions();
  state.regionFilter = regions.some((region) => region.name === name) ? name : "";
  if (els.regionFilter) els.regionFilter.value = state.regionFilter;
  if (state.regionFilter && els.regionSelect) els.regionSelect.value = state.regionFilter;
  state.workspacePage = 1;
  state.topologyPage = 1;
  if (state.mapActive) state.mapSelected = state.regionFilter;
  syncRegionURL();
  renderRegionSummary();
  if (state.mapActive) {
    if (typeof renderMapIfVisible === "function") renderMapIfVisible();
    if (state.regionFilter) {
      if (typeof focusMapRegion === "function") focusMapRegion(state.regionFilter);
    } else if (typeof resetMapView === "function") {
      resetMapView();
    }
  }
  if (!reload) return;
  if (state.topologyActive) {
    if (typeof loadTopology === "function") loadTopology({ silent: true });
    return;
  }
  if (state.mapActive) return;
  if (els.volumesView && !els.volumesView.hidden) {
    if (typeof loadVolumes === "function") loadVolumes({ silent: true });
    return;
  }
  if (typeof load === "function") load({ silent: true });
}
async function loadRegionSummaries({ silent = false } = {}) {
  const regions = configuredRegions();
  if (!regions.length) {
    state.regionSummaries = [];
    renderRegionSummary();
    return;
  }
  const request = ++state.regionSummaryRequest;
  try {
    const data = await api("/api/regions");
    if (request !== state.regionSummaryRequest) return;
    state.regionSummaries = Array.isArray(data) ? data : [];
    renderRegionSummary();
    if (typeof renderMapIfVisible === "function") renderMapIfVisible();
    if (els.regionsDialog && els.regionsDialog.open) {
      renderRegionsList(state.regionSummaries);
    }
  } catch (err) {
    if (!silent) toast(err.message || "Failed to load region details.");
    if (els.regionsDialog && els.regionsDialog.open && els.regionsList) {
      els.regionsList.replaceChildren();
      const message = document.createElement("p");
      message.className = "hint";
      message.textContent = err.message || "Failed to load region details.";
      els.regionsList.append(message);
    }
  }
}
function renderRegionSummary() {
  const nodes = document.querySelectorAll("[data-region-summary]");
  const name = state.regionFilter;
  for (const node of nodes) {
    node.replaceChildren();
    node.hidden = !name;
  }
  if (!name) return;

  const region = configuredRegions().find((item) => item.name === name);
  const summary = regionSummaryFor(name);
  const displayName = region ? region.displayName || region.name : name;
  const available = summary ? Boolean(summary.available) : null;

  const head = document.createElement("div");
  head.className = "region-summary-head";
  const title = document.createElement("strong");
  title.textContent = displayName;
  const slug = document.createElement("span");
  slug.className = "badge neutral";
  slug.textContent = region ? region.name : name;
  head.append(title, slug);
  if (region && region.default) {
    const badge = document.createElement("span");
    badge.className = "badge neutral";
    badge.textContent = "default";
    head.append(badge);
  }
  if (available !== null) {
    const status = document.createElement("span");
    status.className = `region-status ${available ? "ok" : "down"}`;
    status.textContent = available ? "Available" : "Unavailable";
    head.append(status);
  }

  const parts = [];
  if (summary && available) {
    parts.push(`${summary.workspaces || 0} workspace${summary.workspaces === 1 ? "" : "s"}`);
    parts.push(`${summary.running || 0} running`);
    parts.push(`${summary.stopped || 0} stopped`);
  } else if (available === false) {
    parts.push("Region manager unavailable");
  } else {
    const total = Number(state.workspaceTotal) || 0;
    parts.push(`${total} workspace${total === 1 ? "" : "s"}`);
  }
  const sshHost = (summary && summary.sshHost) || (region && region.sshHost) || state.config.sshHost;
  const sshPort = (summary && summary.sshPort) || (region && region.sshPort) || state.config.sshPort;
  if (sshHost) parts.push(`SSH ${sshHost}:${sshPort || "22"}`);

  const meta = document.createElement("div");
  meta.className = "region-summary-meta";
  meta.textContent = parts.join(" · ");

  const body = document.createElement("div");
  body.className = "region-summary-body";
  body.append(head, meta);
  for (const node of nodes) node.append(body.cloneNode(true));
}
function renderRegionsList(summaries) {
  if (!els.regionsList) return;
  els.regionsList.replaceChildren();
  const regions = configuredRegions();
  if (!regions.length) {
    const empty = document.createElement("p");
    empty.className = "hint";
    empty.textContent = "No regions are configured.";
    els.regionsList.append(empty);
    return;
  }
  if (!summaries.length) {
    const loading = document.createElement("div");
    loading.className = "loading-small";
    loading.textContent = "Loading region details...";
    els.regionsList.append(loading);
    return;
  }

  for (const region of regions) {
    const summary = summaries.find((item) => item.name === region.name) || {};
    const available = Boolean(summary.available);
    const card = document.createElement("article");
    card.className = "region-card";

    const header = document.createElement("div");
    header.className = "region-card-header";
    const titleWrap = document.createElement("div");
    titleWrap.className = "region-card-title";
    const title = document.createElement("h3");
    title.textContent = region.displayName || region.name;
    const slug = document.createElement("span");
    slug.className = "badge neutral";
    slug.textContent = region.name;
    titleWrap.append(title, slug);
    if (region.default) {
      const badge = document.createElement("span");
      badge.className = "badge neutral";
      badge.textContent = "default";
      titleWrap.append(badge);
    }
    const status = document.createElement("span");
    status.className = `region-status ${available ? "ok" : "down"}`;
    status.textContent = available ? "Available" : "Unavailable";
    header.append(titleWrap, status);

    const stats = document.createElement("div");
    stats.className = "region-card-stats";
    for (const [value, label] of [
      [available ? summary.workspaces || 0 : "—", "workspaces"],
      [available ? summary.running || 0 : "—", "running"],
      [available ? summary.stopped || 0 : "—", "stopped"],
    ]) {
      const item = document.createElement("div");
      const number = document.createElement("strong");
      number.textContent = String(value);
      const text = document.createElement("span");
      text.textContent = label;
      item.append(number, text);
      stats.append(item);
    }

    const footer = document.createElement("div");
    footer.className = "region-card-footer";
    const sshHost = summary.sshHost || region.sshHost || state.config.sshHost;
    const sshPort = summary.sshPort || region.sshPort || state.config.sshPort;
    if (sshHost) {
      const endpoint = document.createElement("span");
      endpoint.className = "region-card-endpoint";
      endpoint.textContent = `SSH ${sshHost}:${sshPort || "22"}`;
      footer.append(endpoint);
    }
    const view = document.createElement("button");
    view.type = "button";
    view.className = "button ghost small";
    view.textContent = "View workspaces";
    view.disabled = !available;
    view.addEventListener("click", () => {
      if (els.regionsDialog) els.regionsDialog.close();
      state.topologyActive = false;
      state.mapActive = false;
      setRegionFilter(region.name);
      showView("workspaces");
      if (typeof syncWorkspaceViewButtons === "function") syncWorkspaceViewButtons();
    });
    footer.append(view);

    card.append(header, stats, footer);
    els.regionsList.append(card);
  }
}
function openRegions() {
  if (!els.regionsDialog) return;
  renderRegionsList(state.regionSummaries);
  els.regionsDialog.showModal();
  loadRegionSummaries({ silent: true });
}
function renderRegionOptions() {
  if (!els.regionSelect || !els.regionField) return;
  const regions = configuredRegions();
  els.regionSelect.replaceChildren();
  for (const region of regions) {
    const option = document.createElement("option");
    option.value = region.name;
    option.textContent = region.displayName || region.name;
    els.regionSelect.append(option);
  }
  const selected = regions.find((region) => region.name === state.regionFilter)
    || regions.find((region) => region.default)
    || regions[0];
  if (selected) els.regionSelect.value = selected.name;
  els.regionField.hidden = regions.length === 0;
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
    field.className = "field param-field";

    const label = document.createElement("label");
    label.htmlFor = `param-${parameter.name}`;
    label.textContent = parameter.displayName || humanize(parameter.name);

    const type = String(parameter.type || "string").toLowerCase();
    const options = Array.isArray(parameter.options) ? parameter.options : [];
    let control;

    if (options.length) {
      control = document.createElement("select");
      for (const option of options) {
        const choice = document.createElement("option");
        choice.value = String(option.value ?? "");
        choice.textContent = option.label || option.name || option.value || "Default";
        if (option.description) choice.title = option.description;
        control.append(choice);
      }
      control.value = String(parameter.default ?? "");
      if (control.selectedIndex < 0) control.selectedIndex = 0;
    } else if (type === "boolean" || type === "bool") {
      control = document.createElement("select");
      for (const [value, text] of [["true", "Enabled"], ["false", "Disabled"]]) {
        const choice = document.createElement("option");
        choice.value = value;
        choice.textContent = text;
        control.append(choice);
      }
      control.value = String(parameter.default || "false") === "false" ? "false" : "true";
    } else {
      control = document.createElement("input");
      control.type = type === "number" ? "number" : "text";
      if (type === "number") control.step = "any";
      control.value = parameter.default || "";
      control.placeholder = parameter.required ? "Required" : "Optional";
      control.autocomplete = "off";
      control.spellcheck = false;
    }

    control.id = `param-${parameter.name}`;
    control.name = parameter.name;
    if (parameter.required) control.required = true;

    field.append(label, control);
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
function setFormError(element, message) {
  if (!element) return;
  element.textContent = message || "";
  element.hidden = !message;
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
    if (me.email) els.userChip.title = `Account settings — ${me.email}`;

    const owned = Array.isArray(me.ownedTemplates) ? me.ownedTemplates : [];
    els.userRole.hidden = true;
    els.userRole.classList.remove("owner");
    els.manageUsers.hidden = true;
    els.manageTeams.hidden = true;
    els.manageTemplates.hidden = true;
    els.manageAudit.hidden = true;
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
    } else if (me.canViewAudit) {
      els.userRole.hidden = false;
      els.userRole.textContent = "auditor";
    }
    els.manageAudit.hidden = !me.canViewAudit;
  } catch {
    // The server redirects unauthenticated page loads to the login page.
  }
}
function showView(name) {
  els.workspacesView.hidden = name !== "workspaces";
  els.volumesView.hidden = name !== "volumes";
  els.topologyView.hidden = name !== "topology";
  els.mapView.hidden = name !== "map";
  els.connectionsView.hidden = name !== "connections";
  els.sshKeysView.hidden = name !== "ssh-keys";
  els.usersView.hidden = name !== "users";
  els.teamsView.hidden = name !== "teams";
  els.templatesView.hidden = name !== "templates";
  els.templateDetailView.hidden = name !== "template";
  els.auditView.hidden = name !== "audit";
  const active = name === "workspaces" || name === "topology" || name === "map" ? els.manageWorkspaces
    : name === "volumes" ? els.manageVolumes
    : name === "connections" ? els.manageConnections
    : name === "users" ? els.manageUsers
    : name === "teams" ? els.manageTeams
    : (name === "templates" || name === "template") ? els.manageTemplates
    : name === "audit" ? els.manageAudit
    : null;
  for (const button of [els.manageWorkspaces, els.manageVolumes, els.manageConnections, els.manageUsers, els.manageTeams, els.manageTemplates, els.manageAudit]) {
    if (!button) continue;
    button.classList.toggle("active", button === active);
    if (button === active) button.setAttribute("aria-current", "page");
    else button.removeAttribute("aria-current");
  }
  window.scrollTo({ top: 0 });
}
