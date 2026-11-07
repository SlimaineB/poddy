"use strict";

async function loadSSHKeys() {
  const keys = await api("/api/me/ssh-keys");
  state.sshKeys = Array.isArray(keys) ? keys : [];
  renderSSHKeys();
}
function sshKeyRow(key) {
  const row = document.createElement("article");
  row.className = "ssh-key-row";
  const info = document.createElement("div");
  const title = document.createElement("code");
  title.textContent = key.fingerprint || key.id;
  const meta = document.createElement("p");
  meta.className = "hint";
  meta.textContent = `${key.type || "ssh"} key${key.addedAt ? ` · added ${formatAge(key.addedAt)}` : ""}`;
  info.append(title, meta);
  const remove = document.createElement("button");
  remove.type = "button";
  remove.className = "button danger small";
  remove.textContent = "Remove";
  remove.addEventListener("click", () => removeSSHKey(key));
  row.append(info, remove);
  return row;
}
function renderSSHKeys() {
  els.sshKeysList.replaceChildren();
  const keys = state.sshKeys || [];
  els.sshKeysCount.textContent = `${keys.length} key${keys.length === 1 ? "" : "s"}`;
  if (!keys.length) {
    els.sshKeysList.append(emptyInline("No SSH keys yet. Add your public key to connect from VS Code or a terminal."));
    return;
  }
  for (const key of keys) els.sshKeysList.append(sshKeyRow(key));
}
async function addSSHKey(event) {
  event.preventDefault();
  setFormError(els.sshKeyError, "");
  const key = els.sshKeyInput.value.trim();
  if (!key) {
    setFormError(els.sshKeyError, "Paste a public key first.");
    return;
  }
  els.sshKeySave.disabled = true;
  try {
    await api("/api/me/ssh-keys", { method: "POST", body: JSON.stringify({ key }) });
    els.sshKeyInput.value = "";
    els.sshKeyFile.value = "";
    await loadSSHKeys();
    toast("SSH key added.", "success");
  } catch (err) {
    setFormError(els.sshKeyError, err.message || "Failed to add SSH key.");
  } finally {
    els.sshKeySave.disabled = false;
  }
}
async function removeSSHKey(key) {
  if (!window.confirm(`Remove ${key.fingerprint || "this key"}?`)) return;
  try {
    await api(`/api/me/ssh-keys/${encodeURIComponent(key.id)}`, { method: "DELETE" });
    await loadSSHKeys();
    toast("SSH key removed.", "success");
  } catch (err) {
    toast(err.message || "Failed to remove SSH key.");
  }
}
async function openSSHKeys() {
  try {
    await loadSSHKeys();
  } catch (err) {
    toast(err.message || "Failed to load SSH keys.");
    return;
  }
  showView("ssh-keys");
}

els.userChip?.addEventListener("click", openSSHKeys);
els.sshKeysBack?.addEventListener("click", () => showView("workspaces"));
els.sshKeyForm?.addEventListener("submit", addSSHKey);
els.sshKeyFile?.addEventListener("change", async () => {
  const file = els.sshKeyFile.files?.[0];
  if (!file) return;
  if (file.size > 65536) {
    setFormError(els.sshKeyError, "Public key file is too large.");
    return;
  }
  els.sshKeyInput.value = (await file.text()).trim();
  setFormError(els.sshKeyError, "");
});
