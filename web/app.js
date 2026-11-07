"use strict";

async function init() {
  await loadMe();
  await loadDirectory();
  await loadExternalAuth();
  const authError = new URLSearchParams(location.search).get("externalAuthError");
  if (authError) {
    toast(authError);
    const url = new URL(location.href);
    url.searchParams.delete("externalAuthError");
    history.replaceState({}, "", url);
  }
  try {
    const [templates, config] = await Promise.all([api("/api/templates"), api("/api/config")]);
    state.templates = Array.isArray(templates) ? templates : [];
    state.config = config || {};
    renderTemplateOptions();
    renderConfig();
    renderTemplateManager();
  } catch (err) {
    toast(err.message || "Failed to load portal configuration.");
  }

  await load();
  if (new URLSearchParams(location.search).get("view") === "topology") {
    await openTopology();
  }
  setInterval(() => {
    if (!document.hidden) load({ silent: true });
  }, 5000);
}

document.querySelectorAll("[data-close]").forEach((button) => {
  button.addEventListener("click", () => closeDialog(button));
});

document.querySelectorAll("[data-create]").forEach((button) => {
  button.addEventListener("click", openCreate);
});

els.newWorkspace.addEventListener("click", openCreate);

els.refresh.addEventListener("click", async () => {
  els.refresh.classList.add("spinning");
  try {
    await load();
  } finally {
    els.refresh.classList.remove("spinning");
  }
});

els.templateSelect.addEventListener("change", renderParams);
els.createForm.addEventListener("input", () => setFormError(els.createError, ""));

els.createForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  const form = event.target;
  const data = new FormData(form);
  const template = templateByName(els.templateSelect.value);
  const parameters = {};

  if (template) {
    for (const parameter of template.parameters || []) {
      const value = String(data.get(parameter.name) || "").trim();
      if (value !== "") parameters[parameter.name] = value;
    }
  }

  const body = {
    name: String(data.get("name") || "").trim(),
    template: els.templateSelect.value,
    parameters,
  };

  const original = els.createSubmit.innerHTML;
  setFormError(els.createError, "");
  els.createSubmit.disabled = true;
  els.createSubmit.innerHTML = `<span class="spinner small"></span>Creating`;

  try {
    const workspace = await api("/api/workspaces", {
      method: "POST",
      body: JSON.stringify(body),
    });
    els.createDialog.close();
    els.createForm.reset();
    setFormError(els.createError, "");
    toast(`Workspace ${workspace.name} is being created.`, "success");
    await load();
  } catch (err) {
    setFormError(els.createError, err.message || "Failed to create workspace.");
    if (String(err.message || "").includes("external auth")) {
      await loadExternalAuth();
      renderExternalAuthNotice();
    }
  } finally {
    els.createSubmit.disabled = false;
    els.createSubmit.innerHTML = original;
    renderExternalAuthNotice();
  }
});

let workspaceSearchTimer = 0;
els.workspaceSearch.addEventListener("input", () => {
  state.workspaceQuery = els.workspaceSearch.value;
  state.workspacePage = 1;
  clearTimeout(workspaceSearchTimer);
  workspaceSearchTimer = setTimeout(() => load({ silent: true }), 250);
});
els.manageWorkspaces.addEventListener("click", () => {
  state.topologyActive = false;
  showView("workspaces");
  syncWorkspaceViewButtons();
  load({ silent: true });
});
els.manageConnections.addEventListener("click", openConnections);
els.connectionsBack.addEventListener("click", () => showView("workspaces"));
els.manageUsers.addEventListener("click", openUsers);
els.manageTeams.addEventListener("click", openTeams);
els.manageTemplates.addEventListener("click", openTemplates);
els.teamNew.addEventListener("click", newTeam);
els.teamForm.addEventListener("submit", saveTeam);
els.usersBack.addEventListener("click", () => showView("workspaces"));
els.teamsBack.addEventListener("click", () => showView("workspaces"));
let userSearchTimer = 0;
els.usersSearch.addEventListener("input", () => {
  state.userQuery = els.usersSearch.value;
  state.userPage = 1;
  state.groupPage = 1;
  clearTimeout(userSearchTimer);
  userSearchTimer = setTimeout(async () => {
    await loadDirectory({ paginated: true, query: state.userQuery, page: 1, groupPage: 1 });
    renderUsers();
    renderUsersPagination();
    renderGroupsPagination();
  }, 250);
});
els.templateAccessForm.addEventListener("submit", saveAccess);

els.templatesBack.addEventListener("click", () => showView("workspaces"));
els.templateDetailBack.addEventListener("click", () => {
  showView("templates");
  renderTemplateManager();
});
let templateSearchTimer = 0;
els.templatesSearch.addEventListener("input", () => {
  state.templateQuery = els.templatesSearch.value;
  state.templatePage = 1;
  clearTimeout(templateSearchTimer);
  templateSearchTimer = setTimeout(async () => {
    await loadTemplates({ paginated: true, page: 1 });
    renderTemplatesPagination();
  }, 250);
});

els.copySSH.addEventListener("click", copySSHConfig);
els.detailLogsRefresh.addEventListener("click", refreshDetails);
els.detailPortsRefresh.addEventListener("click", refreshDetailPorts);
els.detailPortForm.addEventListener("submit", (event) => {
  event.preventDefault();
  openDetailPort();
});
els.detailDialog.addEventListener("close", () => {
  state.detailWorkspace = null;
  state.detailRequest += 1;
  state.detailPortsRequest += 1;
});

els.openVSCode.addEventListener("click", () => {
  const workspace = state.sshWorkspace;
  if (!workspace) return;
  els.sshDialog.close();
  if (state.config.localVSCode) {
    launchVSCode(workspace, els.openVSCode);
  } else {
    location.href = vscodeURL(workspace.name);
  }
});

els.confirmDelete.addEventListener("click", async () => {
  const workspace = state.pendingDelete;
  if (!workspace) return;

  const original = els.confirmDelete.innerHTML;
  els.confirmDelete.disabled = true;
  els.confirmDelete.innerHTML = `<span class="spinner small"></span>Deleting`;

  try {
    await api(`/api/workspaces/${encodeURIComponent(workspace.name)}`, { method: "DELETE" });
    els.confirmDialog.close();
    state.pendingDelete = null;
    toast(`Workspace ${workspace.name} deleted.`, "success");
    await load();
  } catch (err) {
    toast(err.message || "Failed to delete workspace.");
  } finally {
    els.confirmDelete.disabled = false;
    els.confirmDelete.innerHTML = original;
  }
});

init();



