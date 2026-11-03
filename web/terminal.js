const params = new URLSearchParams(location.search);
const name = params.get("name");
const token = params.get("token") || "";

const statusEl = document.getElementById("status");
const statusText = document.getElementById("status-text");

function setStatus(text, kind) {
  if (!statusEl || !statusText) return;
  statusText.textContent = text;
  statusEl.className = `status ${kind}`;
  const dot = statusEl.querySelector(".dot");
  if (dot) {
    const dotClass = kind === "connected" ? "running" : kind === "disconnected" ? "failed" : "pending";
    dot.className = `dot ${dotClass}`;
  }
}

if (!name) {
  document.getElementById("terminal").textContent = "Missing workspace name.";
  setStatus("disconnected", "disconnected");
  throw new Error("missing workspace name");
}

document.getElementById("title").textContent = name;
document.getElementById("back").href = token ? `/?token=${encodeURIComponent(token)}` : "/";

const term = new Terminal({
  cursorBlink: true,
  fontSize: 14,
  fontFamily: '"SFMono-Regular", ui-monospace, "JetBrains Mono", Menlo, Consolas, monospace',
  theme: {
    background: "#ffffff",
    foreground: "#1f2937",
    cursor: "#2563eb",
    selectionBackground: "#bfdbfe",
  },
});

const fit = new FitAddon.FitAddon();
term.loadAddon(fit);
term.open(document.getElementById("terminal"));
fit.fit();
term.focus();

const proto = location.protocol === "https:" ? "wss:" : "ws:";
let url = `${proto}//${location.host}/ws?name=${encodeURIComponent(name)}`;
if (token) url += `&token=${encodeURIComponent(token)}`;

const ws = new WebSocket(url);
ws.binaryType = "arraybuffer";

const encoder = new TextEncoder();

ws.onopen = () => {
  setStatus("connected", "connected");
  resize();
  term.focus();
};

ws.onmessage = (event) => {
  if (typeof event.data === "string") {
    term.write(event.data);
  } else {
    term.write(new Uint8Array(event.data));
  }
};

ws.onclose = () => {
  setStatus("disconnected", "disconnected");
  term.write("\r\n\x1b[31m[disconnected]\x1b[0m\r\n");
};

ws.onerror = () => {
  setStatus("disconnected", "disconnected");
};

term.onData((data) => {
  if (ws.readyState === WebSocket.OPEN) ws.send(encoder.encode(data));
});

function resize() {
  fit.fit();
  if (ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
  }
}

window.addEventListener("resize", resize);
setTimeout(resize, 100);
