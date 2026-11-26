"use strict";

const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const os = require("os");
const path = require("path");

const { run } = require("./runner");

const node = process.execPath;

test("returns stdout", async () => {
  const out = await run(node, ["-e", "process.stdout.write('ok')"]);
  assert.equal(out, "ok");
});

test("rejects with the stderr and exit code", async () => {
  await assert.rejects(
    run(node, ["-e", "process.stderr.write('boom'); process.exit(2)"]),
    (err) => {
      assert.match(err.message, /boom/);
      assert.equal(err.code, 2);
      return true;
    }
  );
});

test("kills ssh and its ProxyCommand tree on timeout", async () => {
  const marker = path.join(os.tmpdir(), `poddy-runner-${process.pid}-${Date.now()}.tmp`);
  const parent = `
    const { spawn } = require("child_process");
    spawn(process.execPath, ["-e", "setTimeout(() => require('fs').writeFileSync(process.env.PODDY_MARKER, 'alive'), 1500)"], {
      stdio: "ignore",
      env: { ...process.env, PODDY_MARKER: ${JSON.stringify(marker)} },
    });
    setTimeout(() => {}, 60000);
  `;
  await assert.rejects(
    run(node, ["-e", parent], { timeoutMs: 250 }),
    (err) => err.code === "ETIMEDOUT"
  );
  await new Promise((resolve) => setTimeout(resolve, 2000));
  assert.equal(fs.existsSync(marker), false, "grandchild survived the timeout");
});

test("aborts a running probe", async () => {
  const controller = new AbortController();
  const pending = run(node, ["-e", "setTimeout(() => {}, 60000)"], { signal: controller.signal });
  setTimeout(() => controller.abort(), 100);
  await assert.rejects(pending, (err) => err.name === "AbortError");
});
