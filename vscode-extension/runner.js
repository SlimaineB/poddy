"use strict";

const { spawn } = require("child_process");

function run(bin, args, options = {}) {
  const timeoutMs = Number(options.timeoutMs) || 0;
  const signal = options.signal;

  return new Promise((resolve, reject) => {
    if (signal && signal.aborted) {
      reject(abortError());
      return;
    }

    let child;
    try {
      child = spawn(bin, args, {
        windowsHide: true,
        // A separate process group lets us kill ssh and its ProxyCommand
        // together on POSIX. Windows uses taskkill /T instead.
        detached: process.platform !== "win32",
        stdio: ["ignore", "pipe", "pipe"],
      });
    } catch (err) {
      reject(err);
      return;
    }

    let stdout = "";
    let stderr = "";
    let settled = false;
    let timer = null;
    let removeAbort = null;

    const finish = (err) => {
      if (settled) return;
      settled = true;
      if (timer) {
        clearTimeout(timer);
        timer = null;
      }
      if (removeAbort) {
        removeAbort();
        removeAbort = null;
      }
      if (err) reject(err);
      else resolve(stdout);
    };

    const abort = () => {
      killTree(child);
      finish(abortError());
    };

    child.stdout.on("data", (chunk) => {
      stdout += chunk.toString();
    });
    child.stderr.on("data", (chunk) => {
      stderr += chunk.toString();
    });
    child.on("error", (err) => finish(err));
    child.on("close", (code, signalName) => {
      if (settled) return;
      if (code === 0) {
        finish(null);
        return;
      }
      const err = new Error((stderr || stdout || `command failed with code ${code}`).trim());
      err.code = code;
      err.signal = signalName || "";
      err.stdout = stdout;
      err.stderr = stderr;
      finish(err);
    });

    if (signal) {
      const onAbort = () => abort();
      signal.addEventListener("abort", onAbort, { once: true });
      removeAbort = () => signal.removeEventListener("abort", onAbort);
    }

    if (timeoutMs > 0) {
      timer = setTimeout(() => {
        killTree(child);
        const err = new Error(`command timed out after ${Math.round(timeoutMs / 1000)}s`);
        err.code = "ETIMEDOUT";
        finish(err);
      }, timeoutMs);
      if (typeof timer.unref === "function") timer.unref();
    }
  });
}

function killTree(child) {
  if (!child || !child.pid) return;
  if (process.platform === "win32") {
    try {
      child.kill("SIGKILL");
    } catch {}
    try {
      const killer = spawn("taskkill", ["/pid", String(child.pid), "/T", "/F"], {
        windowsHide: true,
        stdio: "ignore",
      });
      killer.on("error", () => {});
    } catch {}
    return;
  }
  try {
    process.kill(-child.pid, "SIGKILL");
  } catch {
    try {
      child.kill("SIGKILL");
    } catch {}
  }
}

function abortError() {
  const err = new Error("command cancelled");
  err.name = "AbortError";
  err.code = "ABORT_ERR";
  return err;
}

module.exports = { run, killTree };
