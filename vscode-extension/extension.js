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

  const keyPath = await ensureKey(payload.workspace);
  const publicKey = await readPublicKey(keyPath);
  const mesh = payload.mesh ? await fetchMesh(payload) : null;
  const alias = await writeSSHConfig(payload, keyPath, context, mesh);
  await postJSON(`${payload.api.replace(/\/$/, "")}/api/desktop/enroll`, {
    ticket: payload.ticket,
    publicKey,
  });

  await vscode.window.withProgress(
    { location: vscode.ProgressLocation.Notification, title: `Poddy: connecting to ${payload.workspace}` },
    () => waitForSSH(alias)
  );

  const remote = vscode.Uri.parse(`vscode-remote://ssh-remote+${alias}/home/dev`);
  await vscode.commands.executeCommand("vscode.openFolder", remote, {
    forceNewWindow: true,
  });
}

function sshDir() {
  return path.join(os.homedir(), ".ssh");
}

function keyPath(workspace) {
  return path.join(sshDir(), `${SSH_KEY_PREFIX}_${workspace}_ed25519`);
}

async function ensureKey(workspace) {
  const target = keyPath(workspace);
  if (fs.existsSync(target)) return target;

  fs.mkdirSync(sshDir(), { recursive: true, mode: 0o700 });
  const candidates = ["ssh-keygen"];
  if (process.platform === "win32") {
    candidates.push("C:\\Windows\\System32\\OpenSSH\\ssh-keygen.exe");
  }
  let lastError;
  for (const bin of candidates) {
    try {
      await run(bin, ["-t", "ed25519", "-N", "", "-C", `poddy-remote:${workspace}`, "-f", target]);
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
  const deadline = Date.now() + 180000;
  let lastError;
  while (Date.now() < deadline) {
    for (const bin of candidates) {
      try {
        await run(bin, [
          "-o", "BatchMode=yes",
          "-o", "ConnectTimeout=30",
          "-o", "StrictHostKeyChecking=no",
          "-o", `UserKnownHostsFile=${NULL_DEVICE}`,
          alias,
          "true",
        ]);
        return;
      } catch (err) {
        lastError = err;
      }
    }
    await new Promise((resolve) => setTimeout(resolve, 3000));
  }
  throw new Error(`workspace SSH is not ready: ${lastError ? lastError.message : "timed out"}`);
}

function postJSON(url, body) {
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
          reject(new Error(text || `HTTP ${res.statusCode}`));
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
        reject(new Error(text || `HTTP ${res.statusCode}`));
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
  const alias = `poddy-desktop-${workspace}`;
  const key = priv.replace(/\\/g, "/");
  const lines = [
    `# >>> poddy:${workspace}`,
    `Host ${alias}`,
    `  HostName ${payload.sshHost}`,
    `  Port ${payload.sshPort}`,
    `  User ${payload.user}`,
    `  IdentityFile "${key}"`,
    "  IdentitiesOnly yes",
  ];
  if (payload.mesh) {
    if (!mesh) throw new Error("missing mesh configuration");
    const helper = await ensureHelper(context);
    const meshDir = path.join(context.globalStorageUri.fsPath, "mesh", workspace);
    fs.mkdirSync(meshDir, { recursive: true, mode: 0o700 });
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
      `  ProxyCommand "${helper}" --mode mesh --control-url "${mesh.controlUrl}" --auth-key-file "${authKeyFile.replace(/\\/g, "/")}" --workspace "${workspace}" --state-dir "${stateDir.replace(/\\/g, "/")}" --hostname "poddy-desktop-${workspace}"${caArg}`,
      "  StrictHostKeyChecking accept-new",
      "  LogLevel ERROR"
    );
  } else {
    const jump = `poddy-desktop-${workspace}-bastion`;
    lines.push(
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
      "  LogLevel ERROR"
    );
  }
  lines.push(`# <<< poddy:${workspace}`, "");
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
  current = removeBlock(current, workspace);
  current = current.trimEnd();
  if (current) current += "\n\n";
  fs.writeFileSync(config, current + block, { mode: 0o600 });
  return alias;
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
  const target = path.join(dir, `poddy-connect-${hash}-${process.pid}${suffix}`);
  if (!fs.existsSync(target)) {
    const tmp = `${target}.${Date.now()}.tmp`;
    fs.copyFileSync(source, tmp);
    fs.renameSync(tmp, target);
  }
  if (goos !== "windows") fs.chmodSync(target, 0o755);
  return target.replace(/\\/g, "/");
}

function removeBlock(content, workspace) {
  const escaped = workspace.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const re = new RegExp(`# >>> poddy:${escaped}\\r?\\n[\\s\\S]*?# <<< poddy:${escaped}\\r?\\n?`, "g");
  return content.replace(re, "");
}

function deactivate() {}

module.exports = { activate, deactivate };
