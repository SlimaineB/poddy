import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { extname, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const webRoot = resolve(fileURLToPath(new URL("../web", import.meta.url)));
const port = Number(process.env.PORT || 4173);

const contentTypes = {
  ".css": "text/css; charset=utf-8",
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".png": "image/png",
  ".svg": "image/svg+xml",
  ".webp": "image/webp",
};

function requestedFile(rawURL) {
  const pathname = new URL(rawURL || "/", "http://localhost").pathname;
  if (pathname === "/") return "index.html";
  if (pathname === "/login") return "login.html";
  if (pathname === "/terminal") return "terminal.html";
  if (pathname.startsWith("/static/")) return pathname.slice("/static/".length);
  return "";
}

const server = createServer(async (request, response) => {
  if (request.url === "/healthz") {
    response.writeHead(200, { "content-type": "text/plain; charset=utf-8" });
    response.end("ok");
    return;
  }

  const relative = requestedFile(request.url);
  const file = relative ? resolve(webRoot, relative) : "";
  if (!file || (file !== webRoot && !file.startsWith(webRoot + sep))) {
    response.writeHead(404, { "content-type": "text/plain; charset=utf-8" });
    response.end("not found");
    return;
  }

  try {
    const body = await readFile(file);
    response.writeHead(200, {
      "cache-control": "no-store",
      "content-type": contentTypes[extname(file)] || "application/octet-stream",
    });
    response.end(body);
  } catch {
    response.writeHead(404, { "content-type": "text/plain; charset=utf-8" });
    response.end("not found");
  }
});

server.listen(port, "127.0.0.1", () => {
  process.stdout.write(`poddy UI test server listening on http://127.0.0.1:${port}\n`);
});
