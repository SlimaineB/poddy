"use strict";

const vscode = require("vscode");
const { execFile } = require("child_process");
const fs = require("fs");
const os = require("os");
const path = require("path");
const http = require("http");
const https = require("https");
const crypto = require("crypto");

const SSH_KEY_PREFIX = "poddy";
const NULL_DEVICE = process.platform === "win32" ? "NUL" : "/dev/null";
// SSH must outlive the mesh join; otherwise ProxyCommand is killed mid-banner.
const MESH_JOIN_TIMEOUT_SECONDS = 30;
const SSH_CONNECT_TIMEOUT = MESH_JOIN_TIMEOUT_SECONDS * 2;
const SSH_WAIT_TIMEOUT_MS = 180000;

function activate(context) {
  context.subscriptions.push(
    vscode.window.registerUriHandler({
      handleUri: (uri) => handleUri(context, uri).catch((err) => {
        vscode.window.showErrorMessage(`Poddy: ${err.message || err}`);
      }),
    })
  );
}

async function handleUri(context, uri) {
  const query = new URLSearchParams(uri.query);
  const encoded = query.get("payload");
  if (!encoded) throw new Error("missing connection payload");

  let payload;
  try {
    payload = JSON.parse(Buffer.from(encoded, "base64url").toString("utf8"));
  } catch {
    throw new Error("invalid connection payload");
  }
  for (const field of ["api", "workspace", "user", "sshHost", "sshPort", "ticket"]) {
    if (!payload[field]) throw new Error(`invalid connection payload (${field})`);
  }
  if (payload.workspace.includes("/") || payload.workspace.includes("\\")) {
    throw new Error("invalid workspace name");
  }

  const keyPath = await ensureKey();
  const publicKey = await readPublicKey(keyPath);
  const useMesh = payload.mesh || payload.transport === "mesh";
  const mesh = useMesh && payload.transport !== "portal" ? await fetchMesh(payload) : null;
  const alias = await writeSSHConfig(payload, keyPath, context, mesh);
  const enrollText = await postJSON(`${payload.api.replace(/\/$/, "")}/api/desktop/enroll`, {
    ticket: payload.ticket,
    publicKey,
  });
  try {
    const enroll = JSON.parse(enrollText);
    console.log(`[poddy] enroll keys=${Object.keys(enroll || {}).join(",")} hasCert=${Boolean(enroll && enroll.certificate)} certLen=${enroll && enroll.certificate ? enroll.certificate.length : 0}`);
    if (enroll && enroll.certificate) {
      const certPath = `${keyPath}-cert.pub`;
      fs.writeFileSync(certPath, enroll.certificate.endsWith("\n") ? enroll.certificate : `${enroll.certificate}\n`, { mode: 0o644 });
      console.log(`[poddy] wrote ${certPath}`);
    }
  } catch (err) {
    console.log(`[poddy] enroll parse error: ${err && err.message}`);
  }

  await vscode.window.withProgress(
    { location: vscode.ProgressLocation.Notification, title: `Poddy: connecting to ${payload.workspace}` },
    () => waitForSSH(alias)
  );
  await pinRemotePlatform(alias);

  const remote = vscode.Uri.parse(`vscode-remote://ssh-remote+${alias}/home/dev`);
  await vscode.commands.executeCommand("vscode.openFolder", remote, {
    forceNewWindow: true,
  });
}

function sshDir() {
  return path.join(os.homedir(), ".ssh");
}

function keyPath() {
  return path.join(sshDir(), `${SSH_KEY_PREFIX}_ed25519`);
}

function legacyKeys() {
  try {
    return fs
      .readdirSync(sshDir())
      .filter((name) => name.startsWith(`${SSH_KEY_PREFIX}_`) && name.endsWith("_ed25519"))
      .filter((name) => name !== `${SSH_KEY_PREFIX}_ed25519`)
      .map((name) => path.join(sshDir(), name))
      .filter((candidate) => fs.existsSync(candidate))
      .sort((a, b) => fs.statSync(b).mtimeMs - fs.statSync(a).mtimeMs);
  } catch {
    return [];
  }
}

function cleanupLegacyKeys(target) {
  for (const legacy of legacyKeys()) {
    if (legacy === target) continue;
    try {
      fs.rmSync(legacy, { force: true });
    } catch {}
    try {
      fs.rmSync(`${legacy}.pub`, { force: true });
    } catch {}
  }
}

function adoptLegacyKey(source, target) {
  fs.copyFileSync(source, target);
  fs.chmodSync(target, 0o600);
  if (fs.existsSync(`${source}.pub`)) {
    fs.copyFileSync(`${source}.pub`, `${target}.pub`);
  }
}

async function ensureKey() {
  const target = keyPath();
  if (fs.existsSync(target)) {
    cleanupLegacyKeys(target);
    return target;
  }

  fs.mkdirSync(sshDir(), { recursive: true, mode: 0o700 });
  for (const legacy of legacyKeys()) {
    try {
      adoptLegacyKey(legacy, target);
      cleanupLegacyKeys(target);
      return target;
    } catch {}
  }

  const candidates = ["ssh-keygen"];
  if (process.platform === "win32") {
    candidates.push("C:\\Windows\\System32\\OpenSSH\\ssh-keygen.exe");
  }
  let lastError;
  for (const bin of candidates) {
    try {
      await run(bin, ["-t", "ed25519", "-N", "", "-C", "poddy-remote", "-f", target]);
      cleanupLegacyKeys(target);
      return target;
    } catch (err) {
      lastError = err;
    }
  }
  throw new Error(`cannot generate an SSH key: ${lastError ? lastError.message : "ssh-keygen not found"}`);
}

async function readPublicKey(priv) {
  const pub = `${priv}.pub`;
  if (fs.existsSync(pub)) return fs.readFileSync(pub, "utf8").trim();

  const candidates = ["ssh-keygen"];
  if (process.platform === "win32") {
    candidates.push("C:\\Windows\\System32\\OpenSSH\\ssh-keygen.exe");
  }
  let lastError;
  for (const bin of candidates) {
    try {
      const out = await run(bin, ["-y", "-f", priv]);
      return out.trim();
    } catch (err) {
      lastError = err;
    }
  }
  throw new Error(`cannot read the SSH public key: ${lastError ? lastError.message : "ssh-keygen not found"}`);
}

function run(bin, args) {
  return new Promise((resolve, reject) => {
    execFile(bin, args, { windowsHide: true }, (err, stdout, stderr) => {
      if (err) {
        err.message = (stderr || stdout || err.message || "").trim();
        reject(err);
        return;
      }
      resolve(stdout || "");
    });
  });
}

async function waitForSSH(alias) {
  const candidates = ["ssh"];
  if (process.platform === "win32") {
    candidates.push("C:\\Windows\\System32\\OpenSSH\\ssh.exe");
  }
  const deadline = Date.now() + SSH_WAIT_TIMEOUT_MS;
  let lastError;
  let fastFailures = 0;
  while (Date.now() < deadline) {
    for (const bin of candidates) {
      const started = Date.now();
      try {
        await run(bin, [
          "-o", "BatchMode=yes",
          "-o", `ConnectTimeout=${SSH_CONNECT_TIMEOUT}`,
          "-o", "StrictHostKeyChecking=no",
          "-o", `UserKnownHostsFile=${NULL_DEVICE}`,
          alias,
          "true",
        ]);
        return;
      } catch (err) {
        lastError = err;
        const message = String(err && err.message || err);
        const retryableKeySync = /permission denied|maxstartups|exceeded maxstartups/i.test(message);
        if (!retryableKeySync && Date.now() - started < 10000) fastFailures += 1; else fastFailures = 0;
      }
    }
    if (fastFailures >= 3) {
      throw new Error(`workspace SSH is not ready: ${lastError ? lastError.message : "proxy command failed"}`);
    }
    await new Promise((resolve) => setTimeout(resolve, 2000));
  }
  throw new Error(`workspace SSH is not ready after 120s: ${lastError ? lastError.message : "timed out"}`);
}

async function pinRemotePlatform(alias) {
  const config = vscode.workspace.getConfiguration("remote.SSH");
  const current = config.get("remotePlatform");
  const platforms = current && typeof current === "object" && !Array.isArray(current) ? current : {};
  if (platforms[alias] === "linux") return;
  try {
    await config.update(
      "remotePlatform",
      { ...platforms, [alias]: "linux" },
      vscode.ConfigurationTarget.Global
    );
  } catch {}
}

function retryableStatus(status) {
  return status === 429 || status === 502 || status === 503 || status === 504;
}

async function withRetry(fn, attempts = 3) {
  let lastError;
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    try {
      return await fn();
    } catch (err) {
      lastError = err;
      if (err && err.statusCode && !retryableStatus(err.statusCode)) throw err;
      if (attempt + 1 >= attempts) break;
      const wait = 1000 * 2 ** attempt + Math.floor(Math.random() * 250);
      await new Promise((resolve) => setTimeout(resolve, wait));
    }
  }
  throw lastError;
}

function postJSON(url, body) {
  return withRetry(() => postJSONOnce(url, body));
}

function postJSONOnce(url, body) {
  return new Promise((resolve, reject) => {
    const target = new URL(url);
    const client = target.protocol === "https:" ? https : http;
    const data = Buffer.from(JSON.stringify(body));
    const req = client.request(
      target,
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Content-Length": data.length,
        },
        timeout: 15000,
      },
      (res) => {
        const chunks = [];
        res.on("data", (chunk) => chunks.push(chunk));
        res.on("end", () => {
          const text = Buffer.concat(chunks).toString("utf8").trim();
          if (res.statusCode >= 200 && res.statusCode < 300) {
            resolve(text);
            return;
          }
          const error = new Error(text || `HTTP ${res.statusCode}`);
          error.statusCode = res.statusCode;
          reject(error);
        });
      }
    );
    req.on("timeout", () => req.destroy(new Error("request timed out")));
    req.on("error", reject);
    // codeql[js/file-access-to-http] The enrollment request intentionally sends the generated SSH public key.
    req.end(data);
  });
}

function getText(url) {
  return withRetry(() => getTextOnce(url));
}

function getTextOnce(url) {
  return new Promise((resolve, reject) => {
    const target = new URL(url);
    const client = target.protocol === "https:" ? https : http;
    const req = client.get(target, { timeout: 15000 }, (res) => {
      const chunks = [];
      res.on("data", (chunk) => chunks.push(chunk));
      res.on("end", () => {
        const text = Buffer.concat(chunks).toString("utf8").trim();
        if (res.statusCode >= 200 && res.statusCode < 300) {
          resolve(text);
          return;
        }
        const error = new Error(text || `HTTP ${res.statusCode}`);
        error.statusCode = res.statusCode;
        reject(error);
      });
    });
    req.on("timeout", () => req.destroy(new Error("request timed out")));
    req.on("error", reject);
  });
}

async function fetchMesh(payload) {
  const api = payload.api.replace(/\/$/, "");
  const target = `${api}/api/desktop/mesh?ticket=${encodeURIComponent(payload.ticket)}`;
  const mesh = JSON.parse(await getText(target));
  if (!mesh.controlUrl || !mesh.authKey) {
    throw new Error("the portal returned an incomplete mesh configuration");
  }
  return mesh;
}

async function writeSSHConfig(payload, priv, context, mesh) {
  const workspace = payload.workspace;
  const useMesh = payload.mesh || payload.transport === "mesh";
  const alias = `poddy-desktop-${workspace}`;
  const key = priv.replace(/\\/g, "/");
  const lines = [];

  if (useMesh || payload.transport === "portal") {
    const helper = await ensureHelper(context);
    lines.push(
      "# >>> poddy",
      "Host poddy-desktop-*",
      "  HostName %h",
      `  Port ${payload.sshPort}`,
      `  User ${payload.user}`,
      `  IdentityFile "${key}"`,
      "  IdentitiesOnly yes",
      `  ConnectTimeout ${SSH_CONNECT_TIMEOUT}`
    );
    if (payload.transport === "portal") {
      const portalDir = path.join(context.globalStorageUri.fsPath, "portal");
      fs.mkdirSync(portalDir, { recursive: true, mode: 0o700 });
      cleanupLegacyDirs(portalDir);
      const ticketFile = path.join(portalDir, `${workspace}.ticket`);
      fs.writeFileSync(ticketFile, `${payload.ticket}\n`, { mode: 0o600 });
      lines.push(
        `  ProxyCommand "${helper}" --portal "${payload.api.replace(/\/$/, "")}" --workspace "%n" --ticket-dir "${portalDir.replace(/\\/g, "/")}" --timeout 15s`,
        "  StrictHostKeyChecking accept-new",
        "  LogLevel ERROR"
      );
    } else {
      if (!mesh) throw new Error("missing mesh configuration");
      const meshDir = path.join(context.globalStorageUri.fsPath, "mesh");
      fs.mkdirSync(meshDir, { recursive: true, mode: 0o700 });
      cleanupLegacyDirs(meshDir, "state");
      const authKeyFile = path.join(meshDir, "authkey");
      fs.writeFileSync(authKeyFile, `${mesh.authKey}\n`, { mode: 0o600 });
      const stateDir = path.join(meshDir, "state");
      let caArg = "";
      if (mesh.caCert) {
        const caFile = path.join(meshDir, "ca.crt");
        fs.writeFileSync(caFile, `${mesh.caCert.trim()}\n`, { mode: 0o600 });
        caArg = ` --ca-file "${caFile.replace(/\\/g, "/")}"`;
      }
      lines.push(
        `  ProxyCommand "${helper}" --mode mesh --control-url "${mesh.controlUrl}" --auth-key-file "${authKeyFile.replace(/\\/g, "/")}" --workspace "%n" --state-dir "${stateDir.replace(/\\/g, "/")}" --hostname "%n" --join-timeout ${MESH_JOIN_TIMEOUT_SECONDS}s${caArg}`,
        "  StrictHostKeyChecking accept-new",
        "  LogLevel ERROR"
      );
    }
    lines.push("# <<< poddy", "");
  } else {
    const jump = `poddy-desktop-${workspace}-bastion`;
    lines.push(
      `# >>> poddy:${workspace}`,
      `Host ${alias}`,
      `  HostName ${payload.sshHost}`,
      `  Port ${payload.sshPort}`,
      `  User ${payload.user}`,
      `  IdentityFile "${key}"`,
      "  IdentitiesOnly yes",
      `  ConnectTimeout ${SSH_CONNECT_TIMEOUT}`,
      `  ProxyCommand ssh -W %h:%p ${jump}`,
      "  StrictHostKeyChecking no",
      `  UserKnownHostsFile ${NULL_DEVICE}`,
      "  LogLevel ERROR",
      "",
      `Host ${jump}`,
      `  HostName ${payload.sshHost}`,
      `  Port ${payload.sshPort}`,
      "  User poddy",
      `  IdentityFile "${key}"`,
      "  IdentitiesOnly yes",
      "  StrictHostKeyChecking accept-new",
      "  LogLevel ERROR",
      `# <<< poddy:${workspace}`,
      ""
    );
  }
  const block = lines.join("\n");

  const dir = sshDir();
  fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
  const config = path.join(dir, "config");
  let current = "";
  try {
    current = fs.readFileSync(config, "utf8");
  } catch (err) {
    if (err.code !== "ENOENT") throw err;
  }
  current = removePoddyBlocks(current);
  current = current.trimEnd();
  if (current) current += "\n\n";
  fs.writeFileSync(config, current + block, { mode: 0o600 });
  return alias;
}

function cleanupLegacyDirs(root, keep) {
  try {
    for (const name of fs.readdirSync(root)) {
      if (keep && name === keep) continue;
      const candidate = path.join(root, name);
      try {
        if (fs.statSync(candidate).isDirectory()) fs.rmSync(candidate, { recursive: true, force: true });
      } catch {}
    }
  } catch {}
}

async function ensureHelper(context) {
  const goos = { linux: "linux", darwin: "darwin", win32: "windows" }[process.platform];
  const goarch = { x64: "amd64", arm64: "arm64" }[process.arch];
  if (!goos || !goarch) {
    throw new Error(`unsupported platform ${process.platform}/${process.arch}`);
  }
  const suffix = goos === "windows" ? ".exe" : "";
  const source = path.join(context.extensionPath, "bin", `poddy-connect-${goos}-${goarch}${suffix}`);
  if (!fs.existsSync(source)) {
    throw new Error(`the extension is missing poddy-connect for ${goos}/${goarch}; reinstall the latest VSIX`);
  }
  const dir = path.join(context.globalStorageUri.fsPath, "bin");
  fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
  const hash = crypto.createHash("sha256").update(fs.readFileSync(source)).digest("hex").slice(0, 12);
  const target = path.join(dir, `poddy-connect-${hash}${suffix}`);
  if (!fs.existsSync(target)) {
    const tmp = `${target}.${Date.now()}.tmp`;
    fs.copyFileSync(source, tmp);
    fs.renameSync(tmp, target);
  }
  if (goos !== "windows") fs.chmodSync(target, 0o755);
  return target.replace(/\\/g, "/");
}

function removePoddyBlocks(content) {
  return content.replace(/# >>> poddy(?::[^\r\n]*)?\r?\n[\s\S]*?# <<< poddy(?::[^\r\n]*)?\r?\n?/g, "");
}

function deactivate() {}

module.exports = { activate, deactivate };
