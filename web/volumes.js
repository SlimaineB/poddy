"use strict";

function formatVolumeBytes(bytes) {
  const value = Number(bytes);
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  let index = 0;
  let size = value;
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024;
    index += 1;
  }
  const rounded = size >= 10 || index === 0 ? Math.round(size) : size.toFixed(1);
  return `${rounded} ${units[index]}`;
}

function volumeRegionParam() {
  return state.regionFilter ? `?region=${encodeURIComponent(state.regionFilter)}` : "";
}

function volumeActionURL(volume, suffix = "") {
  const region = volume.region || state.regionFilter;
  const query = region ? `?region=${encodeURIComponent(region)}` : "";
  return `/api/volumes/${encodeURIComponent(volume.name)}${suffix}${query}`;
}

function openVolumes() {
  state.topologyActive = false;
  state.mapActive = false;
  showView("volumes");
  loadVolumes();
}

async function loadVolumes({ silent = false } = {}) {
  if (!silent) els.volumesLoading.hidden = false;
  els.volumesRefresh.disabled = true;
  try {
    const data = await api(`/api/volumes${volumeRegionParam()}`);
    if (Array.isArray(data)) {
      state.volumes = data;
      state.volumeSummary = summarizeVolumes(data);
      state.volumeBackupEnabled = false;
    } else {
      state.volumes = Array.isArray(data.volumes) ? data.volumes : [];
      state.volumeSummary = data.summary || summarizeVolumes(state.volumes);
      state.volumeBackupEnabled = Boolean(data.backup);
    }
    renderVolumes();
  } catch (err) {
    toast(err.message || "Failed to load volumes.");
  } finally {
    els.volumesLoading.hidden = true;
    els.volumesRefresh.disabled = false;
  }
}

function summarizeVolumes(volumes) {
  const summary = { total: 0, assigned: 0, unassigned: 0, bytes: 0, assignedBytes: 0, unassignedBytes: 0 };
  for (const volume of volumes) {
    summary.total += 1;
    summary.bytes += Number(volume.bytes) || 0;
    if (volume.assigned) {
      summary.assigned += 1;
      summary.assignedBytes += Number(volume.bytes) || 0;
    } else {
      summary.unassigned += 1;
      summary.unassignedBytes += Number(volume.bytes) || 0;
    }
  }
  return summary;
}

function renderVolumes() {
  const query = state.volumeQuery.trim().toLowerCase();
  const volumes = state.volumes.filter((volume) => {
    if (state.volumeFilter === "assigned" && !volume.assigned) return false;
    if (state.volumeFilter === "unassigned" && volume.assigned) return false;
    if (!query) return true;
    return [volume.name, volume.workspace, volume.owner, volume.region, volume.phase, volume.storageClass, volume.workspacePhase]
      .filter(Boolean)
      .some((value) => String(value).toLowerCase().includes(query));
  });

  renderVolumeSummary();
  els.volumesCount.textContent = `${volumes.length} volume${volumes.length === 1 ? "" : "s"}`;
  els.volumesList.replaceChildren();
  els.volumesEmpty.hidden = volumes.length > 0;
  els.volumesListWrap.hidden = volumes.length === 0;

  if (!volumes.length) {
    const summary = state.volumeSummary || {};
    if (Number(summary.total) > 0) {
      els.volumesEmpty.querySelector("h2").textContent = "No volumes match this filter";
      els.volumesEmpty.querySelector("p").textContent = "Try another search or filter.";
    } else {
      els.volumesEmpty.querySelector("h2").textContent = "No volumes found";
      els.volumesEmpty.querySelector("p").textContent = "Persistent workspaces create a volume automatically. Orphaned volumes appear here after their workspace is deleted.";
    }
    return;
  }

  const fragment = document.createDocumentFragment();
  for (const volume of volumes) fragment.append(renderVolumeRow(volume));
  els.volumesList.append(fragment);
}

function renderVolumeSummary() {
  const summary = state.volumeSummary || summarizeVolumes(state.volumes);
  const cards = [
    { label: "Total volumes", value: String(summary.total || 0), hint: formatVolumeBytes(summary.bytes) },
    { label: "Assigned", value: String(summary.assigned || 0), hint: formatVolumeBytes(summary.assignedBytes) },
    { label: "Unassigned", value: String(summary.unassigned || 0), hint: formatVolumeBytes(summary.unassignedBytes) },
  ];
  els.volumesSummary.replaceChildren();
  for (const card of cards) {
    const node = document.createElement("div");
    node.className = "volume-summary-card";
    const label = document.createElement("span");
    label.className = "volume-summary-label";
    label.textContent = card.label;
    const value = document.createElement("strong");
    value.textContent = card.value;
    const hint = document.createElement("span");
    hint.className = "volume-summary-hint";
    hint.textContent = card.hint;
    node.append(label, value, hint);
    els.volumesSummary.append(node);
  }
}

function renderVolumeRow(volume) {
  const row = document.createElement("tr");

  const nameCell = document.createElement("td");
  const name = document.createElement("span");
  name.className = "volume-name";
  name.textContent = volume.name;
  name.title = volume.name;
  nameCell.append(name);
  if (!volume.assigned) {
    const badge = document.createElement("span");
    badge.className = "badge warning";
    badge.textContent = "orphan";
    nameCell.append(badge);
  }

  const workspaceCell = document.createElement("td");
  if (volume.assigned && volume.workspace) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "workspace-name-button";
    button.textContent = volume.workspace;
    button.title = `Open ${volume.workspace}`;
    button.addEventListener("click", () => showDetails({ name: volume.workspace }));
    workspaceCell.append(button);
  } else {
    const text = document.createElement("span");
    text.className = "volume-muted";
    text.textContent = "Unassigned";
    workspaceCell.append(text);
  }

  const ownerCell = document.createElement("td");
  ownerCell.className = "workspace-owner";
  ownerCell.textContent = volume.owner || "";
  ownerCell.title = volume.owner || "";

  const regionCell = document.createElement("td");
  regionCell.textContent = volume.region || "local";

  const statusCell = document.createElement("td");
  const status = document.createElement("span");
  status.className = "status";
  const dot = document.createElement("span");
  dot.className = `dot ${statusClass(volume.assigned ? volume.workspacePhase : volume.phase)}`;
  status.append(dot, document.createTextNode(volume.phase || "Unknown"));
  statusCell.append(status);
  if (volume.assigned && volume.workspacePhase) {
    const detail = document.createElement("div");
    detail.className = "volume-muted";
    detail.textContent = volume.workspacePhase === "Stopped" ? "Workspace stopped" : `Workspace ${volume.workspacePhase}`;
    statusCell.append(detail);
  }

  const capacityCell = document.createElement("td");
  capacityCell.textContent = formatVolumeBytes(volume.bytes);
  if (volume.requested) capacityCell.title = `Requested ${volume.requested}`;

  const classCell = document.createElement("td");
  classCell.textContent = volume.storageClass || "default";

  const actionsCell = document.createElement("td");
  actionsCell.className = "workspace-table-actions";
  const actions = document.createElement("div");
  actions.className = "workspace-actions volume-actions";

  if (state.volumeBackupEnabled) {
    const backups = document.createElement("button");
    backups.type = "button";
    backups.className = "button small";
    backups.innerHTML = `${icon("database")}Backups`;
    backups.title = "Create, restore, or delete volume backups";
    backups.addEventListener("click", () => openVolumeBackups(volume));
    actions.append(backups);
  }

  if (state.me && state.me.isAdmin && !volume.assigned) {
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "icon-button danger";
    remove.title = "Delete unassigned volume";
    remove.setAttribute("aria-label", `Delete ${volume.name}`);
    remove.innerHTML = icon("trash");
    remove.addEventListener("click", () => openVolumeDelete(volume));
    actions.append(remove);
  }

  actionsCell.append(actions);
  row.append(nameCell, workspaceCell, ownerCell, regionCell, statusCell, capacityCell, classCell, actionsCell);
  return row;
}

function openVolumeBackups(volume) {
  state.volumeBackupVolume = volume;
  state.volumeBackups = [];
  state.volumeBackupRequest += 1;
  els.volumeBackupsTitle.textContent = `Backups · ${volume.name}`;
  els.volumeBackupsSubtitle.textContent = volume.assigned
    ? `Persistent volume for workspace ${volume.workspace}.`
    : "Orphaned persistent volume.";
  els.volumeBackupsList.replaceChildren();
  els.restoreError.hidden = true;
  renderVolumeBackupStatus();
  if (!els.volumeBackupsDialog.open) els.volumeBackupsDialog.showModal();
  loadVolumeBackups();
}

function renderVolumeBackupStatus() {
  const volume = state.volumeBackupVolume;
  els.volumeBackupStatus.replaceChildren();
  if (!volume) return;
  const ready = !volume.assigned || volume.workspacePhase === "Stopped";
  const note = document.createElement("p");
  note.className = `volume-backup-note${ready ? " ready" : " blocked"}`;
  note.textContent = ready
    ? "Ready. Backups run in a temporary helper pod and do not change the workspace."
    : `Stop the workspace first. The volume is currently attached to a ${volume.workspacePhase || "running"} workspace.`;
  els.volumeBackupStatus.append(note);
  els.volumeBackupCreate.disabled = !ready || !state.volumeBackupEnabled;
}

async function loadVolumeBackups() {
  const volume = state.volumeBackupVolume;
  if (!volume) return;
  const request = ++state.volumeBackupRequest;
  els.volumeBackupsRefresh.disabled = true;
  setVolumeBackupsMessage("Loading backups...");
  try {
    const backups = await api(volumeActionURL(volume, "/backups"));
    if (request !== state.volumeBackupRequest) return;
    state.volumeBackups = Array.isArray(backups) ? backups : [];
    renderVolumeBackups();
  } catch (err) {
    if (request !== state.volumeBackupRequest) return;
    setVolumeBackupsMessage(err.message || "Could not load backups.");
  } finally {
    if (request === state.volumeBackupRequest) els.volumeBackupsRefresh.disabled = false;
  }
}

function setVolumeBackupsMessage(message) {
  els.volumeBackupsList.replaceChildren();
  const note = document.createElement("p");
  note.className = "hint";
  note.textContent = message;
  els.volumeBackupsList.append(note);
}

function renderVolumeBackups() {
  els.volumeBackupsList.replaceChildren();
  if (!state.volumeBackups.length) {
    setVolumeBackupsMessage("No backups yet. Create one to archive this volume in object storage.");
    return;
  }
  const list = document.createElement("div");
  list.className = "volume-backups";
  for (const backup of state.volumeBackups) {
    const item = document.createElement("div");
    item.className = "volume-backup-item";

    const meta = document.createElement("div");
    meta.className = "volume-backup-meta";
    const title = document.createElement("strong");
    title.textContent = backup.name || backup.key;
    const details = document.createElement("span");
    const when = backup.lastModified ? new Date(backup.lastModified) : null;
    const date = when && !Number.isNaN(when.getTime()) ? when.toLocaleString() : "";
    details.textContent = [formatVolumeBytes(backup.size), date].filter(Boolean).join(" · ");
    meta.append(title, details);

    const actions = document.createElement("div");
    actions.className = "volume-backup-item-actions";
    const restore = document.createElement("button");
    restore.type = "button";
    restore.className = "button ghost small";
    restore.textContent = "Restore";
    restore.addEventListener("click", () => openRestoreBackup(backup));
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "icon-button danger";
    remove.title = "Delete backup";
    remove.setAttribute("aria-label", `Delete backup ${backup.name || backup.key}`);
    remove.innerHTML = icon("trash");
    remove.addEventListener("click", () => deleteVolumeBackup(backup));
    actions.append(restore, remove);

    item.append(meta, actions);
    list.append(item);
  }
  els.volumeBackupsList.append(list);
}

async function createVolumeBackup() {
  const volume = state.volumeBackupVolume;
  if (!volume) return;
  const original = els.volumeBackupCreate.innerHTML;
  els.volumeBackupCreate.disabled = true;
  els.volumeBackupCreate.innerHTML = `<span class="spinner small"></span>Creating backup`;
  try {
    await api(volumeActionURL(volume, "/backups"), { method: "POST" });
    toast(`Backup created for ${volume.name}.`, "success");
    await loadVolumeBackups();
  } catch (err) {
    toast(err.message || "Failed to create backup.");
  } finally {
    els.volumeBackupCreate.innerHTML = original;
    renderVolumeBackupStatus();
  }
}

function openRestoreBackup(backup) {
  const volume = state.volumeBackupVolume;
  if (!volume) return;
  state.volumeRestoreKey = backup.key;
  state.volumeRestoreClean = false;
  els.restoreText.textContent = `Restore ${backup.name || backup.key} into ${volume.name}?`;
  els.restoreClean.checked = false;
  els.restoreError.hidden = true;
  els.restoreDialog.showModal();
}

async function confirmRestoreBackup() {
  const volume = state.volumeBackupVolume;
  const key = state.volumeRestoreKey;
  if (!volume || !key) return;
  const original = els.restoreConfirm.innerHTML;
  els.restoreConfirm.disabled = true;
  els.restoreConfirm.innerHTML = `<span class="spinner small"></span>Restoring`;
  els.restoreError.hidden = true;
  try {
    await api(volumeActionURL(volume, "/restore"), {
      method: "POST",
      body: JSON.stringify({ key, clean: els.restoreClean.checked }),
    });
    els.restoreDialog.close();
    toast(`Backup restored into ${volume.name}.`, "success");
    await loadVolumeBackups();
  } catch (err) {
    els.restoreError.hidden = false;
    els.restoreError.textContent = err.message || "Failed to restore backup.";
  } finally {
    els.restoreConfirm.disabled = false;
    els.restoreConfirm.innerHTML = original;
  }
}

async function deleteVolumeBackup(backup) {
  const volume = state.volumeBackupVolume;
  if (!volume) return;
  if (!window.confirm(`Delete backup ${backup.name || backup.key}? This cannot be undone.`)) return;
  try {
    const query = new URLSearchParams({ key: backup.key });
    const region = volume.region || state.regionFilter;
    if (region) query.set("region", region);
    await api(`/api/volumes/${encodeURIComponent(volume.name)}/backups?${query.toString()}`, { method: "DELETE" });
    toast("Backup deleted.", "success");
    await loadVolumeBackups();
  } catch (err) {
    toast(err.message || "Failed to delete backup.");
  }
}

function openVolumeDelete(volume) {
  state.pendingVolumeDelete = volume;
  els.volumeDeleteText.textContent = `Permanently delete ${volume.name}? All data on this unassigned volume will be lost.`;
  if (!els.volumeDeleteDialog.open) els.volumeDeleteDialog.showModal();
}

async function confirmVolumeDelete() {
  const volume = state.pendingVolumeDelete;
  if (!volume) return;
  const original = els.volumeDeleteConfirm.innerHTML;
  els.volumeDeleteConfirm.disabled = true;
  els.volumeDeleteConfirm.innerHTML = `<span class="spinner small"></span>Deleting`;
  try {
    await api(volumeActionURL(volume), { method: "DELETE" });
    els.volumeDeleteDialog.close();
    state.pendingVolumeDelete = null;
    toast(`Volume ${volume.name} deleted.`, "success");
    await loadVolumes({ silent: true });
  } catch (err) {
    toast(err.message || "Failed to delete volume.");
  } finally {
    els.volumeDeleteConfirm.disabled = false;
    els.volumeDeleteConfirm.innerHTML = original;
  }
}

els.manageVolumes.addEventListener("click", openVolumes);
els.volumesBack.addEventListener("click", () => showView("workspaces"));
els.volumesRefresh.addEventListener("click", () => loadVolumes());
els.volumesSearch.addEventListener("input", () => {
  state.volumeQuery = els.volumesSearch.value;
  renderVolumes();
});
document.querySelectorAll("[data-volume-filter]").forEach((button) => {
  button.addEventListener("click", () => {
    state.volumeFilter = button.dataset.volumeFilter;
    document.querySelectorAll("[data-volume-filter]").forEach((item) => {
      const active = item === button;
      item.classList.toggle("active", active);
      item.setAttribute("aria-pressed", String(active));
    });
    renderVolumes();
  });
});
els.volumeBackupsRefresh.addEventListener("click", loadVolumeBackups);
els.volumeBackupCreate.addEventListener("click", createVolumeBackup);
els.restoreConfirm.addEventListener("click", confirmRestoreBackup);
els.volumeDeleteConfirm.addEventListener("click", confirmVolumeDelete);
els.volumeBackupsDialog.addEventListener("close", () => {
  state.volumeBackupVolume = null;
  state.volumeBackupRequest += 1;
  state.volumeBackups = [];
});
els.restoreDialog.addEventListener("close", () => {
  state.volumeRestoreKey = "";
  els.restoreError.hidden = true;
});
els.volumeDeleteDialog.addEventListener("close", () => {
  state.pendingVolumeDelete = null;
});
