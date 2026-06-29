// Zero-dependency static server for the built Quartz site, with a token-gated rebuild trigger.
//
// Two responsibilities, one process:
//   - Serve /srv/site at / with Quartz's clean URLs (/library/foo -> library/foo.html) and a
//     404.html fallback — mirroring the express.static mount this image replaces. NO auth on
//     content: a browser session is authenticated by the proxy in front (e.g. Authelia).
//   - POST /rebuild — restage + rebuild on demand. This IS auth-gated (a machine-to-machine call
//     from the host's content jobs) with a shared-secret bearer; serving reads the atomically
//     swapped output dir, so a rebuild never exposes a half-built tree.
import { createServer } from "node:http";
import { stat } from "node:fs/promises";
import { createReadStream } from "node:fs";
import { spawn } from "node:child_process";
import { join, normalize, extname } from "node:path";
import { timingSafeEqual } from "node:crypto";

const SITE_OUT = "/srv/site";
const PORT = Number(process.env.KNOWLEDGE_SITE_PORT || 8080);
const REBUILD_TOKEN = process.env.KNOWLEDGE_SITE_REBUILD_TOKEN || "";

const TYPES = {
  ".html": "text/html; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".mjs": "text/javascript; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".map": "application/json; charset=utf-8",
  ".xml": "application/xml; charset=utf-8",
  ".txt": "text/plain; charset=utf-8",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".gif": "image/gif",
  ".webp": "image/webp",
  ".avif": "image/avif",
  ".ico": "image/x-icon",
  ".woff": "font/woff",
  ".woff2": "font/woff2",
  ".ttf": "font/ttf",
};

const log = (...a) => console.log(new Date().toISOString(), "site:", ...a);
const typeOf = (f) => TYPES[extname(f).toLowerCase()] || "application/octet-stream";

// --- rebuild trigger ----------------------------------------------------------------------------
let building = false;

function runBuild() {
  return new Promise((resolve) => {
    const p = spawn("/opt/site/build.sh", { stdio: "inherit" });
    p.on("close", (code) => resolve(code === 0));
    p.on("error", () => resolve(false));
  });
}

function bearerOK(req) {
  if (!REBUILD_TOKEN) return false; // can't authenticate without a configured secret
  const m = /^Bearer (.+)$/.exec(req.headers["authorization"] || "");
  if (!m) return false;
  const got = Buffer.from(m[1]);
  const want = Buffer.from(REBUILD_TOKEN);
  return got.length === want.length && timingSafeEqual(got, want);
}

// --- static resolution --------------------------------------------------------------------------
// Contain the resolved path within SITE_OUT (no `..` escape), then try the candidates that
// reproduce Quartz's clean-URL shape.
function resolveCandidates(urlPath) {
  const reqPath = decodeURIComponent(urlPath.split("?")[0]);
  const full = normalize(join(SITE_OUT, reqPath));
  if (full !== SITE_OUT && !full.startsWith(SITE_OUT + "/")) return [];
  if (reqPath.endsWith("/")) return [join(full, "index.html")];
  return [
    full, // exact asset or real .html
    full + ".html", // clean URL: /library/foo -> library/foo.html
    join(full, "index.html"), // folder page without trailing slash
  ];
}

async function resolveFile(urlPath) {
  for (const c of resolveCandidates(urlPath)) {
    try {
      if ((await stat(c)).isFile()) return c;
    } catch {
      /* try next */
    }
  }
  return null;
}

function sendFile(res, file, code, headOnly) {
  res.writeHead(code, { "content-type": typeOf(file) });
  if (headOnly) return res.end();
  createReadStream(file).pipe(res);
}

// --- server -------------------------------------------------------------------------------------
const server = createServer(async (req, res) => {
  try {
    const path = (req.url || "/").split("?")[0];

    if (req.method === "POST" && path === "/rebuild") {
      if (!REBUILD_TOKEN) return res.writeHead(503).end("rebuild disabled: no token configured\n");
      if (!bearerOK(req)) return res.writeHead(401).end("unauthorized\n");
      if (building) return res.writeHead(202).end("rebuild already in progress\n");
      building = true;
      log("rebuild requested");
      // Respond immediately; the caller (a content job) treats this as fire-and-forget.
      res.writeHead(202).end("rebuild started\n");
      runBuild().then((ok) => {
        building = false;
        log(ok ? "rebuild done" : "rebuild FAILED — previous site kept");
      });
      return;
    }

    if (req.method !== "GET" && req.method !== "HEAD") {
      return res.writeHead(405).end("method not allowed\n");
    }

    const headOnly = req.method === "HEAD";
    const file = await resolveFile(req.url || "/");
    if (file) return sendFile(res, file, 200, headOnly);

    // Fall through to Quartz's 404.html, like the express.static mount did.
    const notFound = join(SITE_OUT, "404.html");
    try {
      await stat(notFound);
      return sendFile(res, notFound, 404, headOnly);
    } catch {
      res.writeHead(404, { "content-type": "text/plain; charset=utf-8" });
      return res.end(headOnly ? undefined : "404 Not Found\n");
    }
  } catch (err) {
    log("request error:", err?.message || err);
    if (!res.headersSent) res.writeHead(500).end("internal error\n");
  }
});

server.listen(PORT, () => {
  log(`serving ${SITE_OUT} on :${PORT}`);
  if (!REBUILD_TOKEN) log("warning: KNOWLEDGE_SITE_REBUILD_TOKEN unset — POST /rebuild disabled (503)");
});
