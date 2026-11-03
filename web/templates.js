"use strict";

async function loadTemplates() {
  const templates = await api("/api/templates");
  state.templates = Array.isArray(templates) ? templates : [];
  renderTemplateOptions();
  renderTemplateManager();
}
function renderTemplateManager() {
  if (!els.templatesPageList) return;
  const query = state.templateQuery.trim().toLowerCase();
  const manageable = state.templates.filter((template) => template.canManage);
  const filtered = manageable.filter((template) => {
    if (!query) return true;
    return [template.name, template.displayName, template.description, template.image, template.version]
      .some((value) => String(value || "").toLowerCase().includes(query));
  });

  els.templatesCount.textContent = `${filtered.length} template${filtered.length === 1 ? "" : "s"}`;
  els.templatesPageList.replaceChildren();
  if (!filtered.length) {
    els.templatesPageList.append(emptyInline(query ? "No templates match your search." : "No templates to manage."));
    return;
  }

  const limit = 200;
  for (const template of filtered.slice(0, limit)) {
    const item = document.createElement("button");
    item.type = "button";
    item.className = "template-page-item";

    const main = document.createElement("span");
    main.className = "template-page-main";

    const heading = document.createElement("span");
    heading.className = "template-page-heading";
    const name = document.createElement("span");
    name.className = "template-name";
    name.textContent = template.displayName || template.name;
    const key = document.createElement("code");
    key.className = "template-key";
    key.textContent = template.name;
    heading.append(name, key);

    const description = document.createElement("span");
    description.className = "template-description";
    description.textContent = template.description || "No description.";

    const meta = document.createElement("span");
    meta.className = "template-meta";
    if (template.image) {
      const image = document.createElement("code");
      image.textContent = template.image;
      meta.append(image);
    }
    if (template.version) {
      const version = document.createElement("span");
      version.className = "badge neutral";
      version.textContent = `v${template.version}`;
      meta.append(version);
    }
    if (template.owned) {
      const badge = document.createElement("span");
      badge.className = "badge owner";
      badge.textContent = "owner";
      meta.append(badge);
    }

    const action = document.createElement("span");
    action.className = "template-open";
    action.textContent = "Manage access →";

    main.append(heading, description, meta);
    item.append(main, action);
    item.addEventListener("click", () => openAccess(template));
    els.templatesPageList.append(item);
  }

  if (filtered.length > limit) {
    els.templatesPageList.append(emptyInline(`Showing the first ${limit} of ${filtered.length} templates. Refine your search to narrow the list.`));
  }
}
async function openTemplates() {
  try {
    await loadTemplates();
    state.templateQuery = "";
    els.templatesSearch.value = "";
    showView("templates");
    renderTemplateManager();
  } catch (err) {
    toast(err.message || "Failed to load templates.");
  }
}
async function openAccess(template) {
  try {
    const [access, teams] = await Promise.all([
      api(`/api/templates/${encodeURIComponent(template.name)}/access`),
      api("/api/teams").catch(() => []),
      loadDirectory(),
    ]);
    state.accessTemplate = template;
    state.accessCanEditOwners = Boolean(access.canEditOwners);
    state.teams = Array.isArray(teams) ? teams : [];
    els.templateDetailTitle.textContent = template.displayName || template.name;
    const details = [template.description, template.version ? `Version ${template.version}` : "", template.image];
    els.templateDetailDescription.textContent = details.filter(Boolean).join(" · ");
    entryLists.owners.setValues(access.owners || []);
    entryLists.owners.setReadOnly(!state.accessCanEditOwners);
    entryLists.users.setValues(access.users || []);
    renderAccessTeams(access.teams || []);
    showView("template");
  } catch (err) {
    toast(err.message || "Failed to load template access.");
  }
}
function renderAccessTeams(selected) {
  els.accessTeams.replaceChildren();
  if (!state.teams.length) {
    els.accessTeams.append(emptyInline("No teams yet. Create teams from the Teams button."));
    return;
  }
  const chosen = new Set(selected);
  for (const team of state.teams) {
    const label = document.createElement("label");
    label.className = "team-check";
    const input = document.createElement("input");
    input.type = "checkbox";
    input.value = team.name;
    input.checked = chosen.has(team.name);
    const text = document.createElement("span");
    text.textContent = team.name;
    label.append(input, text);
    els.accessTeams.append(label);
  }
}
async function saveAccess(event) {
  event.preventDefault();
  const template = state.accessTemplate;
  if (!template) return;
  const users = entryLists.users.getValues();
  const teams = [...els.accessTeams.querySelectorAll("input:checked")].map((input) => input.value);
  const body = { users, teams };
  if (state.accessCanEditOwners) body.owners = entryLists.owners.getValues();

  const button = els.accessSave;
  const original = button.innerHTML;
  button.disabled = true;
  button.innerHTML = `<span class="spinner small"></span>Saving`;
  try {
    await api(`/api/templates/${encodeURIComponent(template.name)}/access`, {
      method: "PUT",
      body: JSON.stringify(body),
    });
    toast(`Access for ${template.displayName || template.name} saved.`, "success");
    await loadMe();
    await loadTemplates();
  } catch (err) {
    toast(err.message || "Failed to save template access.");
  } finally {
    button.disabled = false;
    button.innerHTML = original;
  }
}
