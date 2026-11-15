"use strict";

async function loadDotfiles() {
  const settings = await api("/api/me/dotfiles");
  state.dotfiles = settings || {};
  renderDotfiles();
}

function renderDotfiles() {
  const settings = state.dotfiles || {};
  const source = settings.source || "";
  els.dotfilesURL.value = settings.url || "";
  els.dotfilesBranch.value = settings.branch || "main";
  els.dotfilesSource.textContent = source === "user" ? "Personal" : source === "global" ? "Global default" : "Not configured";
  els.dotfilesClear.hidden = source !== "user";
}

async function saveDotfiles(event) {
  event.preventDefault();
  setFormError(els.dotfilesError, "");
  const url = els.dotfilesURL.value.trim();
  const branch = els.dotfilesBranch.value.trim() || "main";
  if (!url) {
    setFormError(els.dotfilesError, "Enter a Git repository URL, or use the global default.");
    return;
  }
  els.dotfilesSave.disabled = true;
  try {
    const settings = await api("/api/me/dotfiles", {
      method: "PUT",
      body: JSON.stringify({ url, branch }),
    });
    state.dotfiles = settings || {};
    renderDotfiles();
    toast("Dotfiles saved. Restart running workspaces to apply them.", "success");
  } catch (err) {
    setFormError(els.dotfilesError, err.message || "Failed to save dotfiles.");
  } finally {
    els.dotfilesSave.disabled = false;
  }
}

async function clearDotfiles() {
  els.dotfilesClear.disabled = true;
  setFormError(els.dotfilesError, "");
  try {
    await api("/api/me/dotfiles", { method: "DELETE" });
    await loadDotfiles();
    toast("Using the global dotfiles default.", "success");
  } catch (err) {
    setFormError(els.dotfilesError, err.message || "Failed to clear dotfiles.");
  } finally {
    els.dotfilesClear.disabled = false;
  }
}

els.dotfilesForm?.addEventListener("submit", saveDotfiles);
els.dotfilesClear?.addEventListener("click", clearDotfiles);
