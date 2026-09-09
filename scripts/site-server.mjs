// Shared loopback server for website verification and the social-card renderer.
import { createServer } from "node:http";
import { readFile, stat } from "node:fs/promises";
import { createRequire } from "node:module";
import { execFileSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
export const source = path.join(root, "site");
export const site = path.join(root, "dist", "site");
export const siteRequire = createRequire(path.join(source, "package.json"));
const types = { ".html": "text/html", ".css": "text/css", ".js": "text/javascript", ".svg": "image/svg+xml", ".png": "image/png", ".jpg": "image/jpeg", ".woff2": "font/woff2", ".xml": "application/xml" };

export async function serveSite() {
  execFileSync("python3", [path.join(root, "scripts", "build-site.py")], { cwd: root, stdio: "inherit" });
  const server = createServer(async (req, res) => {
    try {
      const url = new URL(req.url, "http://localhost");
      if (!url.pathname.startsWith("/eagent/")) {
        res.writeHead(404); res.end("Not found"); return;
      }
      const relative = decodeURIComponent(url.pathname.slice("/eagent/".length)) || "index.html";
      let filename = path.resolve(site, relative);
      if (!filename.startsWith(site + path.sep)) {
        res.writeHead(403); res.end("Forbidden"); return;
      }
      // GitHub Pages serves a directory's index.html; match that behavior so
      // clean games/<preset>/ URLs resolve in tests like they do in production.
      try {
        if ((await stat(filename)).isDirectory()) filename = path.join(filename, "index.html");
      } catch {
        // Missing paths fall through to the branded 404 below.
      }
      const data = await readFile(filename);
      res.setHeader("Content-Type", (types[path.extname(filename)] || "application/octet-stream") + ([".html", ".css", ".js"].includes(path.extname(filename)) ? "; charset=utf-8" : ""));
      res.end(data);
    } catch {
      res.writeHead(404, { "Content-Type": "text/html; charset=utf-8" });
      res.end(await readFile(path.join(site, "404.html")));
    }
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  return { server, base: "http://127.0.0.1:" + server.address().port + "/eagent/" };
}
